package sessionnative

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/builtinruntime"
	"google.golang.org/adk/internal/mcpruntime"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/sandboxruntime"
	"google.golang.org/adk/internal/skillruntime"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/tool"
)

// Manager binds a Runtime session to an executor-only Sandbox lease.
// The Agent loop always remains in AISphere Runtime; Sandbox contributes
// workspace/executor capabilities through its tools endpoint.
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
	RuntimeConfig  *runtimeconfig.Config

	mu     sync.Mutex
	leases map[string]*SessionLease
}

type CreateSessionRequest struct {
	AppName           string
	UserID            string
	SessionID         string
	ProjectID         string
	AgentID           string
	SnapshotID        string
	Profile           string
	TemplateRef       string
	WarmPoolRef       string
	Version           string
	ApprovalConfirmed bool
	ApprovedTools     []string
	SkipAgentResolve  bool
	Reuse             bool
	State             map[string]any
}

type SessionLease struct {
	SessionID  string
	AgentID    string
	SnapshotID string
	Profile    string
	Sandbox    *sandboxclient.Lease
	Plan       *runtimeplan.RuntimePlan
	SkillRoot  string
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
	if existing := m.leases[key]; existing != nil && existing.Sandbox != nil &&
		strings.TrimSpace(existing.Sandbox.ToolsEndpoint) != "" &&
		(req.SkipAgentResolve || m.Hub == nil || !m.Hub.Enabled() || existing.Plan != nil) {
		m.mu.Unlock()
		return existing, nil
	}
	m.mu.Unlock()

	profile := strings.TrimSpace(req.Profile)
	templateRef := strings.TrimSpace(req.TemplateRef)
	warmPoolRef := strings.TrimSpace(req.WarmPoolRef)
	snapshotID := strings.TrimSpace(req.SnapshotID)
	var snapshot *aihubruntime.AgentSnapshot
	var plan *runtimeplan.RuntimePlan

	if m.Hub != nil && m.Hub.Enabled() && !req.SkipAgentResolve {
		snap, err := m.Hub.ResolveAgentSnapshotWithOptions(ctx, agentID, req.SessionID, aihubruntime.AgentResolveOptions{
			Version: req.Version, ApprovalConfirmed: req.ApprovalConfirmed, ApprovedTools: req.ApprovedTools,
		})
		if err != nil {
			return nil, fmt.Errorf("resolve Hub agent snapshot: %w", err)
		}
		if strings.TrimSpace(m.SkillsRoot) != "" {
			if err := m.Hub.CacheAgentSnapshotSkills(ctx, m.SkillsRoot, snap); err != nil {
				return nil, err
			}
		}
		agentPlan, err := runtimeplan.FromSnapshot(snap)
		if err != nil {
			return nil, fmt.Errorf("build runtime plan: %w", err)
		}
		snapshot = snap
		plan = agentPlan
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
		// Transitional metadata consumed by the current executor/tool-server
		// surface. Agent definition/model execution is deliberately not copied
		// into Sandbox: the Agent loop and model stay in Runtime.
		metadata["runtimePlan"] = runtimePlanMetadata(plan)
		metadata["allowedTools"] = allowedTools(plan, snapshot)
		metadata["toolSchemas"] = toolSchemas(plan, snapshot)
		metadata["skillRefs"] = skillRefs(plan, snapshot)
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
	timeout := m.ReadyTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The Agent Sandbox controller may publish a Service name in the initial
	// ensure response before EndpointSlice DNS and the tool server are reachable.
	// Always perform an active readiness check; accepting a syntactically present
	// toolsEndpoint produces a lease that fails on the first real tool call.
	lease, err = m.waitSandboxToolsReady(waitCtx, lease.SandboxID, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(lease.ToolsEndpoint) == "" {
		return nil, fmt.Errorf("sandbox %s has no executor tools endpoint", lease.SandboxID)
	}

	if snapshot != nil {
		if err := m.materializeSnapshot(ctx, lease, snapshot); err != nil {
			return nil, err
		}
	}

	skillRoot := ""
	if snapshot != nil && len(snapshot.Skills) > 0 {
		if strings.TrimSpace(m.SkillsRoot) == "" {
			return nil, fmt.Errorf("runtime requires a configured skills root")
		}
		var err error
		skillRoot, err = aihubruntime.PrepareAgentSnapshotSkillRoot(m.SkillsRoot, snapshot)
		if err != nil {
			return nil, err
		}
	}

	out := &SessionLease{
		SessionID:  req.SessionID,
		AgentID:    agentID,
		SnapshotID: snapshotID,
		Profile:    profile,
		Sandbox:    lease,
		Plan:       plan,
		SkillRoot:  skillRoot,
	}
	m.mu.Lock()
	if m.leases == nil {
		m.leases = map[string]*SessionLease{}
	}
	m.leases[key] = out
	m.mu.Unlock()
	return out, nil
}

func (m *Manager) waitSandboxToolsReady(ctx context.Context, sandboxID string, interval time.Duration) (*sandboxclient.Lease, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		lease, err := m.Sandbox.Get(ctx, sandboxID)
		if err == nil && (strings.EqualFold(lease.Phase, "ready") || strings.EqualFold(lease.Phase, "running")) && strings.TrimSpace(lease.ToolsEndpoint) != "" {
			if _, probeErr := m.Sandbox.ListTools(ctx, sandboxID); probeErr == nil {
				return lease, nil
			} else {
				lastErr = probeErr
			}
		} else if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("sandbox %s tools did not become ready: %w", sandboxID, lastErr)
			}
			return nil, ctx.Err()
		case <-ticker.C:
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

// materializeSnapshot copies only Skill payloads that sandbox executors may
// need. Agent definitions, model configuration and Agent-loop state remain in
// Runtime and are never materialized into the Sandbox workspace.
func (m *Manager) materializeSnapshot(ctx context.Context, lease *sandboxclient.Lease, snapshot *aihubruntime.AgentSnapshot) error {
	if lease == nil || snapshot == nil || strings.TrimSpace(lease.ToolsEndpoint) == "" {
		return nil
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

func (m *Manager) ToolRegistryForLease(lease *SessionLease) (*toolruntime.Registry, error) {
	if m == nil || m.Sandbox == nil {
		return nil, fmt.Errorf("native sandbox client is not configured")
	}
	if lease == nil || lease.Sandbox == nil || strings.TrimSpace(lease.Sandbox.SandboxID) == "" {
		return nil, fmt.Errorf("session has no sandbox lease")
	}
	registry := toolruntime.New()
	if err := registry.Register("sandbox", sandboxruntime.Resolver{
		Caller:     m.Sandbox,
		SandboxID:  lease.Sandbox.SandboxID,
		SnapshotID: lease.SnapshotID,
		SessionID:  lease.SessionID,
	}); err != nil {
		return nil, err
	}
	if m.RuntimeConfig != nil {
		if err := registry.Register("internal", builtinruntime.Resolver{Config: m.RuntimeConfig}); err != nil {
			return nil, err
		}
		if err := registry.RegisterToolset("mcp", mcpruntime.Resolver{Config: m.RuntimeConfig}); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (m *Manager) ToolsetsForLease(ctx context.Context, lease *SessionLease) ([]tool.Toolset, error) {
	if lease == nil || lease.Plan == nil || len(lease.Plan.Skills) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(lease.SkillRoot) == "" {
		return nil, fmt.Errorf("session has no materialized runtime skill root")
	}
	set, err := skillruntime.NewToolset(ctx, lease.SkillRoot, lease.Plan.Skills)
	if err != nil {
		return nil, err
	}
	if set == nil {
		return nil, nil
	}
	return []tool.Toolset{set}, nil
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
			"toolsEndpoint":   l.Sandbox.ToolsEndpoint,
			"browserEndpoint": l.Sandbox.BrowserEndpoint,
			"workspace":       l.Sandbox.Workspace,
			"expiresAt":       l.Sandbox.ExpiresAt,
			"runtimePlan":     runtimePlanMetadata(l.Plan),
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
