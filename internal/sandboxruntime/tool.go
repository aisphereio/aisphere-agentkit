// Package sandboxruntime adapts sandbox-hosted tools into ADK-Go tools.
package sandboxruntime

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

type ToolCaller interface {
	CallTool(ctx context.Context, sandboxID string, req sandboxclient.ToolCallRequest) (*sandboxclient.ToolCallResult, error)
}

type Resolver struct {
	Caller     ToolCaller
	SandboxID  string
	SnapshotID string
	SessionID  string
}

func (r Resolver) ResolveTool(binding runtimeplan.ToolBinding) (tool.Tool, error) {
	if r.Caller == nil {
		return nil, fmt.Errorf("sandbox tool caller is required")
	}
	if strings.TrimSpace(r.SandboxID) == "" {
		return nil, fmt.Errorf("sandbox id is required")
	}
	if strings.TrimSpace(binding.Name) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	return &sandboxTool{caller: r.Caller, sandboxID: r.SandboxID, snapshotID: r.SnapshotID, sessionID: r.SessionID, binding: binding}, nil
}

type sandboxTool struct {
	caller     ToolCaller
	sandboxID  string
	snapshotID string
	sessionID  string
	binding    runtimeplan.ToolBinding
}

func (t *sandboxTool) Name() string {
	return firstNonEmpty(t.binding.RuntimeName, t.binding.Name)
}

func (t *sandboxTool) Description() string {
	if value := stringFromMap(t.binding.Runtime, "description"); value != "" {
		return value
	}
	if value := stringFromMap(t.binding.Metadata, "description"); value != "" {
		return value
	}
	return "Execute authorized sandbox tool " + t.Name()
}

func (t *sandboxTool) IsLongRunning() bool {
	return strings.EqualFold(stringFromMap(t.binding.Execution, "mode"), "long_running") ||
		strings.EqualFold(stringFromMap(t.binding.Metadata, "longRunning"), "true")
}

func (t *sandboxTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:                 t.Name(),
		Description:          t.Description(),
		ParametersJsonSchema: maps.Clone(t.binding.InputSchema),
		ResponseJsonSchema:   maps.Clone(t.binding.OutputSchema),
	}
}

func (t *sandboxTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return toolutils.PackTool(req, t)
}

func (t *sandboxTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	input, ok := args.(map[string]any)
	if !ok && args != nil {
		return nil, fmt.Errorf("sandbox tool %s expected map args, got %T", t.Name(), args)
	}
	if input == nil {
		input = map[string]any{}
	}
	callCtx := context.Background()
	runID := ""
	if ctx != nil {
		callCtx = ctx
		runID = ctx.InvocationID()
	}
	result, err := t.caller.CallTool(callCtx, t.sandboxID, sandboxclient.ToolCallRequest{
		Tool:          t.Name(),
		Input:         mapStringInterface(input),
		RunID:         runID,
		TimeoutMillis: t.binding.TimeoutMillis,
		Metadata: map[string]interface{}{
			"runtimeType":  "sandbox",
			"toolId":       t.binding.Name,
			"runtimeName":  t.binding.RuntimeName,
			"toolVersion":  t.binding.Version,
			"toolRevision": t.binding.Revision,
			"capabilities": t.binding.Capabilities,
			"permissions":  t.binding.Permissions,
			"snapshotId":   t.snapshotID,
			"sessionId":    t.sessionID,
		},
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{"ok": true}, nil
	}
	if !result.OK {
		return nil, fmt.Errorf("sandbox tool %s failed: %v", t.Name(), result.Error)
	}
	if result.Result == nil {
		return map[string]any{"ok": true}, nil
	}
	return mapAny(result.Result), nil
}

func stringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mapStringInterface(in map[string]any) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mapAny(in map[string]interface{}) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
