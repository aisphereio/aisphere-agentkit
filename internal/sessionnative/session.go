package sessionnative

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/sessionworkerclient"
)

type Manager struct {
	Sandbox *sandboxclient.Client
	Hub     *aihubruntime.Client

	RuntimeID      string
	SkillsRoot     string
	OrgID          string
	ProjectID      string
	UserID         string
	DefaultProfile string
	ReadyTimeout   time.Duration

	mu     sync.Mutex
	leases map[string]*SessionLease
}

type CreateSessionRequest struct {
	AppName     string
	UserID      string
	SessionID   string
	ProjectID   string
	AgentID     string
	SnapshotID  string
	Profile     string
	TemplateRef string
	WarmPoolRef string
	Reuse       bool
	State       map[string]any
}

type SessionLease struct {
	SessionID  string
	AgentID    string
	SnapshotID string
	Profile    string
	Sandbox    *sandboxclient.Lease
}

func (m *Manager) Enabled() bool { return m != nil && m.Sandbox != nil }

func (m *Manager) CreateSession(ctx context.Context, req CreateSessionRequest) (*SessionLease, error) {
	return m.EnsureSession(ctx, req)
}

func (m *Manager) EnsureSession(ctx context.Context, req CreateSessionRequest) (*SessionLease, error) {
	if !m.Enabled() {
		return nil, fmt.Errorf("native sandbox session manager is disabled")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session id is required for native sandbox sessions")
	}
	agentID := firstNonEmpty(req.AgentID, req.AppName)
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("agent id/app name is required")
	}
	key := cacheKey(agentID, req.UserID, req.SessionID)
	m.mu.Lock()
	if existing := m.leases[key]; existing != nil && existing.Sandbox != nil && existing.Sandbox.WorkerEndpoint != "" {
		m.mu.Unlock()
		return existing, nil
	}
	m.mu.Unlock()

	profile := strings.TrimSpace(req.Profile)
	templateRef := strings.TrimSpace(req.TemplateRef)
	warmPoolRef := strings.TrimSpace(req.WarmPoolRef)
	snapshotID := strings.TrimSpace(req.SnapshotID)
	var snapshot *aihubruntime.AgentSnapshot

	if m.Hub != nil && m.Hub.Enabled() {
		snap, err := m.Hub.ResolveAgentSnapshot(ctx, agentID, req.SessionID)
		if err != nil {
			return nil, fmt.Errorf("resolve Hub agent snapshot: %w", err)
		}
		if strings.TrimSpace(m.SkillsRoot) != "" {
			if err := m.Hub.CacheAgentSnapshotSkills(ctx, m.SkillsRoot, snap); err != nil {
				return nil, err
			}
		}
		snapshot = snap
		if snap.AgentID != "" {
			agentID = snap.AgentID
		}
		snapshotID = firstNonEmpty(snapshotID, snap.SnapshotID)
		profile = firstNonEmpty(profile, snap.Sandbox.Profile)
		templateRef = firstNonEmpty(templateRef, snap.Sandbox.TemplateRef)
		warmPoolRef = firstNonEmpty(warmPoolRef, snap.Sandbox.WarmPoolRef)
	}
	profile = firstNonEmpty(profile, m.DefaultProfile)
	if profile == "" && templateRef == "" {
		return nil, fmt.Errorf("sandbox profile or templateRef is required")
	}

	projectID := firstNonEmpty(req.ProjectID, projectIDFromState(req.State), m.ProjectID)
	userID := firstNonEmpty(req.UserID, m.UserID)
	metadata := map[string]interface{}{
		"appName":   req.AppName,
		"projectId": projectID,
		"native":    true,
	}
	if snapshot != nil {
		metadata["agentDefinition"] = map[string]interface{}{
			"entryPoint": snapshot.Definition.EntryPoint,
			"files":      snapshot.Definition.Files,
			"model":      resolvedModelSpec(snapshot),
		}
		allowedTools := make([]string, 0, len(snapshot.Tools))
		toolSchemas := make([]map[string]interface{}, 0, len(snapshot.Tools))
		skillRefs := make([]map[string]string, 0, len(snapshot.Skills))
		for _, item := range snapshot.Tools {
			if strings.TrimSpace(item.Name) != "" {
				allowedTools = append(allowedTools, strings.TrimSpace(item.Name))
				toolSchemas = append(toolSchemas, map[string]interface{}{
					"name":        item.Name,
					"description": toolDescription(item),
					"inputSchema": item.InputSchema,
					"version":     item.Version,
					"revision":    item.Revision,
				})
			}
		}
		for _, item := range snapshot.Skills {
			if strings.TrimSpace(item.Name) != "" {
				skillRefs = append(skillRefs, map[string]string{"name": item.Name, "version": item.Version})
			}
		}
		metadata["allowedTools"] = allowedTools
		metadata["toolSchemas"] = toolSchemas
		metadata["skillRefs"] = skillRefs
		metadata["snapshotPolicy"] = snapshot.Policy
	}
	lease, err := m.Sandbox.Ensure(ctx, sandboxclient.EnsureRequest{
		RuntimeID:   m.RuntimeID,
		SessionID:   req.SessionID,
		OrgID:       m.OrgID,
		ProjectID:   projectID,
		UserID:      userID,
		AgentID:     agentID,
		SnapshotID:  snapshotID,
		Profile:     profile,
		TemplateRef: templateRef,
		WarmPoolRef: warmPoolRef,
		Reuse:       true,
		Metadata:    metadata,
	})
	if err != nil {
		return nil, err
	}
	if lease.WorkerEndpoint == "" {
		timeout := m.ReadyTimeout
		if timeout <= 0 {
			timeout = 90 * time.Second
		}
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		lease, err = m.Sandbox.WaitReady(waitCtx, lease.SandboxID, 2*time.Second)
		if err != nil {
			return nil, err
		}
	}
	if lease.WorkerEndpoint == "" {
		return nil, fmt.Errorf("sandbox %s has no worker endpoint", lease.SandboxID)
	}
	w := sessionworkerclient.New(lease.WorkerEndpoint, lease.LeaseToken)
	readyCtx, cancel := context.WithTimeout(ctx, m.readyTimeout())
	defer cancel()
	if err := waitWorkerReady(readyCtx, w, time.Second); err != nil {
		return nil, err
	}
	if snapshot != nil {
		if err := m.materializeSnapshot(ctx, lease, snapshot); err != nil {
			return nil, err
		}
	}
	out := &SessionLease{SessionID: req.SessionID, AgentID: agentID, SnapshotID: snapshotID, Profile: profile, Sandbox: lease}
	m.mu.Lock()
	if m.leases == nil {
		m.leases = map[string]*SessionLease{}
	}
	m.leases[key] = out
	m.mu.Unlock()
	return out, nil
}

func waitWorkerReady(ctx context.Context, worker *sessionworkerclient.Client, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := worker.Ready(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func toolDescription(item aihubruntime.ToolSnapshotItem) string {
	if item.Runtime != nil {
		if value, ok := item.Runtime["description"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	if item.Metadata != nil {
		if value, ok := item.Metadata["description"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return "Authorized Agent tool"
}

// materializeSnapshot transfers only the already-authorized Skill payload into
// the session workspace. Tool authorization is carried in the sandbox
// manifest and consumed by the worker; the worker never receives Hub tokens.
func (m *Manager) materializeSnapshot(ctx context.Context, lease *sandboxclient.Lease, snapshot *aihubruntime.AgentSnapshot) error {
	if lease == nil || snapshot == nil || strings.TrimSpace(lease.ToolsEndpoint) == "" {
		return nil
	}
	if err := m.materializeAgentDefinition(ctx, lease, snapshot); err != nil {
		return err
	}
	for _, item := range snapshot.Skills {
		if strings.TrimSpace(item.CachePath) == "" || strings.TrimSpace(item.Name) == "" {
			continue
		}
		if _, err := os.Stat(item.CachePath); err != nil {
			return fmt.Errorf("cached skill %s is unavailable: %w", item.Name, err)
		}
		if err := m.copySkillToSandbox(ctx, lease, item.CachePath, item.Name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) materializeAgentDefinition(ctx context.Context, lease *sandboxclient.Lease, snapshot *aihubruntime.AgentSnapshot) error {
	if snapshot == nil || len(snapshot.Definition.Files) == 0 {
		return nil
	}
	agentID := filesystemName(firstNonEmpty(snapshot.AgentID, "agent"))
	base := filepath.ToSlash(filepath.Join(".aisphere", "agents", agentID))
	for rawPath, content := range snapshot.Definition.Files {
		rel, err := cleanDefinitionPath(rawPath)
		if err != nil {
			return fmt.Errorf("invalid agent definition path %q: %w", rawPath, err)
		}
		if len(content) > 512*1024 {
			return fmt.Errorf("agent definition file %s exceeds 512KiB sandbox materialization limit", rel)
		}
		if err := m.writeWorkspaceFile(ctx, lease, filepath.ToSlash(filepath.Join(base, rel)), content); err != nil {
			return fmt.Errorf("materialize agent definition %s: %w", rel, err)
		}
	}
	manifest := map[string]any{
		"agentId":       snapshot.AgentID,
		"snapshotId":    snapshot.SnapshotID,
		"agentVersion":  snapshot.AgentVersion,
		"agentRevision": snapshot.AgentRevision,
		"entryPoint":    snapshot.Definition.EntryPoint,
		"model":         resolvedModelSpec(snapshot),
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	return m.writeWorkspaceFile(ctx, lease, filepath.ToSlash(filepath.Join(base, "agent-snapshot.json")), string(b))
}

func resolvedModelSpec(snapshot *aihubruntime.AgentSnapshot) aihubruntime.ModelSpec {
	if snapshot == nil {
		return aihubruntime.ModelSpec{}
	}
	model := snapshot.Model
	if model.Profile == "" && model.Model == "" && model.Provider == "" && model.BaseURL == "" && model.APIFormat == "" && len(model.Metadata) == 0 {
		model = snapshot.Definition.Model
	}
	return model
}

func (m *Manager) copySkillToSandbox(ctx context.Context, lease *sandboxclient.Lease, source, name string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || strings.HasPrefix(filepath.Clean(rel), "..") {
			return fmt.Errorf("invalid cached skill path %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(data) > 512*1024 {
			return fmt.Errorf("skill file %s exceeds 512KiB sandbox materialization limit", rel)
		}
		dest := filepath.ToSlash(filepath.Join(".aisphere", "skills", name, rel))
		if err := m.writeWorkspaceFile(ctx, lease, dest, string(data)); err != nil {
			return fmt.Errorf("materialize skill %s: %w", name, err)
		}
		return nil
	})
}

func (m *Manager) writeWorkspaceFile(ctx context.Context, lease *sandboxclient.Lease, path, content string) error {
	deadline := time.Now().Add(m.readyTimeout())
	for {
		result, err := m.Sandbox.CallTool(ctx, lease.SandboxID, sandboxclient.ToolCallRequest{
			Tool:  "workspace.write",
			Input: map[string]interface{}{"path": path, "content": content},
		})
		if err == nil && (result == nil || result.OK) {
			return nil
		}
		if err == nil && result != nil && !result.OK {
			err = fmt.Errorf("workspace.write failed: %v", result.Error)
		}
		if !isTransientSandboxCallError(err) || !time.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *Manager) readyTimeout() time.Duration {
	if m != nil && m.ReadyTimeout > 0 {
		return m.ReadyTimeout
	}
	return 90 * time.Second
}

func isTransientSandboxCallError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "temporary failure") ||
		strings.Contains(msg, "server misbehaving") ||
		strings.Contains(msg, "status=502")
}

func cleanDefinitionPath(raw string) (string, error) {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("path must be relative and stay within the agent definition")
	}
	return cleaned, nil
}

func filesystemName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "agent"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "agent"
	}
	return out
}

func (m *Manager) WorkerClient(lease *SessionLease) (*sessionworkerclient.Client, error) {
	if lease == nil || lease.Sandbox == nil || lease.Sandbox.WorkerEndpoint == "" {
		return nil, fmt.Errorf("session has no native sandbox worker endpoint")
	}
	return sessionworkerclient.New(lease.Sandbox.WorkerEndpoint, lease.Sandbox.LeaseToken), nil
}

func (l *SessionLease) StateDelta() map[string]any {
	if l == nil || l.Sandbox == nil {
		return nil
	}
	return map[string]any{
		"__agent_native_sandbox__": map[string]any{
			"enabled":         true,
			"sessionId":       l.SessionID,
			"agentId":         l.AgentID,
			"snapshotId":      l.SnapshotID,
			"profile":         l.Profile,
			"sandboxId":       l.Sandbox.SandboxID,
			"phase":           l.Sandbox.Phase,
			"driver":          l.Sandbox.Driver,
			"workerEndpoint":  l.Sandbox.WorkerEndpoint,
			"toolsEndpoint":   l.Sandbox.ToolsEndpoint,
			"browserEndpoint": l.Sandbox.BrowserEndpoint,
			"workspace":       l.Sandbox.Workspace,
			"expiresAt":       l.Sandbox.ExpiresAt,
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func projectIDFromState(state map[string]any) string {
	for _, key := range []string{"project_id", "projectId"} {
		if value, ok := state[key]; ok {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func cacheKey(appName, userID, sessionID string) string {
	return strings.TrimSpace(appName) + ":" + strings.TrimSpace(userID) + ":" + strings.TrimSpace(sessionID)
}
