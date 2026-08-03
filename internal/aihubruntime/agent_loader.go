package aihubruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/configurable"
	"google.golang.org/adk/internal/runtimeconfig"
	"gopkg.in/yaml.v3"
)

// SessionAgentLoader materializes only the Hub snapshot that was authorized
// for the current HTTP request. It deliberately has no filesystem fallback:
// an enabled Hub Agent mode must not make locally discovered agents visible.
type SessionAgentLoader struct {
	client *Client
	cfg    *runtimeconfig.Config
	mu     sync.Mutex
}

func NewSessionAgentLoader(client *Client, cfg *runtimeconfig.Config) (*SessionAgentLoader, error) {
	if client == nil || !client.Enabled() {
		return nil, fmt.Errorf("aihub client must be enabled for agent mode")
	}
	if cfg == nil {
		return nil, fmt.Errorf("runtime config is required for agent mode")
	}
	return &SessionAgentLoader{client: client, cfg: cfg}, nil
}

func (l *SessionAgentLoader) ListAgents() []string { return nil }

func (l *SessionAgentLoader) HubManaged() bool { return true }

func (l *SessionAgentLoader) LoadAgent(name string) (agent.Agent, error) {
	return nil, fmt.Errorf("Hub agents require request-scoped loading")
}

func (l *SessionAgentLoader) RootAgent() agent.Agent { return nil }

func (l *SessionAgentLoader) ListAgentsForRequest(ctx context.Context) ([]string, error) {
	items, err := l.client.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			names = append(names, item.ID)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (l *SessionAgentLoader) LoadAgentForRequest(ctx context.Context, name, sessionID string) (agent.Agent, error) {
	snapshot, err := l.client.ResolveAgentSnapshot(ctx, name, sessionID)
	if err != nil {
		return nil, err
	}
	if snapshot.AgentID == "" || snapshot.AgentVersion == "" {
		return nil, fmt.Errorf("Hub returned an incomplete agent snapshot")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	root, err := l.materializeSnapshot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	cfgCopy := *l.cfg
	cfgCopy.Skills.Root = filepath.Join(root, "skills")
	return configurable.FromConfig(runtimeconfig.WithConfig(ctx, &cfgCopy), filepath.Join(root, "agent", filepath.FromSlash(snapshot.Definition.EntryPoint)))
}

func (l *SessionAgentLoader) materializeSnapshot(ctx context.Context, snapshot *AgentSnapshot) (string, error) {
	root := filepath.Join(l.sessionsRoot(), "aihub", safePath(snapshot.SessionID), safePath(snapshot.AgentID), safePath(snapshot.AgentVersion))
	agentRoot := filepath.Join(root, "agent")
	if err := writeAgentFiles(agentRoot, snapshot.Definition); err != nil {
		return "", err
	}
	if err := bindSnapshotSkills(agentRoot, snapshot.Definition.EntryPoint, snapshot.Skills); err != nil {
		return "", err
	}
	skillRoot := filepath.Join(root, "skills")
	for i := range snapshot.Skills {
		item := &snapshot.Skills[i]
		if _, err := l.client.ensureCached(ctx, l.cfg.Skills.Root, item); err != nil {
			return "", fmt.Errorf("cache skill %s@%s: %w", item.Name, item.Version, err)
		}
		if err := copyDir(item.CachePath, filepath.Join(skillRoot, safePath(item.Name))); err != nil {
			return "", fmt.Errorf("mount skill %s@%s: %w", item.Name, item.Version, err)
		}
	}
	return root, nil
}

// bindSnapshotSkills makes Hub authorization visible to configurable.FromConfig.
// Copying files alone is insufficient: ADK's skilltoolset only exposes the
// selected names declared in the agent YAML.
func bindSnapshotSkills(root, entryPoint string, skills []SkillSnapshotItem) error {
	if len(skills) == 0 {
		return nil
	}
	path := filepath.Join(root, filepath.FromSlash(entryPoint))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse agent entrypoint: %w", err)
	}
	selected := make([]string, 0, len(skills))
	seen := map[string]bool{}
	if raw, ok := document["skills"].([]interface{}); ok {
		for _, value := range raw {
			if name, ok := value.(string); ok && strings.TrimSpace(name) != "" && !seen[name] {
				seen[name] = true
				selected = append(selected, name)
			}
		}
	}
	for _, item := range skills {
		name := strings.TrimSpace(item.Name)
		if name != "" && !seen[name] {
			seen[name] = true
			selected = append(selected, name)
		}
	}
	document["skills"] = selected
	updated, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode agent entrypoint: %w", err)
	}
	return os.WriteFile(path, updated, 0o644)
}

func (l *SessionAgentLoader) sessionsRoot() string {
	root := strings.TrimSpace(l.cfg.Skills.AIHub.Sandbox.SessionsRoot)
	if root == "" {
		root = filepath.Join(l.cfg.Root, ".adk", "runtime", "sessions")
	}
	return root
}

func writeAgentFiles(root string, definition AgentDefinition) error {
	if strings.TrimSpace(definition.EntryPoint) == "" || !safeRelativePath(definition.EntryPoint) {
		return fmt.Errorf("invalid agent entry point")
	}
	if _, ok := definition.Files[definition.EntryPoint]; !ok {
		return fmt.Errorf("agent snapshot does not include its entry point")
	}
	for name, content := range definition.Files {
		if !safeRelativePath(name) {
			return fmt.Errorf("invalid agent file path")
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func safeRelativePath(value string) bool {
	value = filepath.ToSlash(strings.TrimSpace(value))
	return value != "" && !strings.HasPrefix(value, "/") && !strings.Contains(value, "..") && filepath.ToSlash(filepath.Clean(value)) == value
}
