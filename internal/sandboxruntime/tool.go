// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package sandboxruntime adapts sandbox-hosted executor capabilities into
// model-callable ADK-Go tools. The model-facing Tool name and the Sandbox
// executor capability are deliberately separate contracts.
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

// Name is the model-facing Tool/function name. During the map-based migration
// RuntimeName may still override the catalog id; typed Tool V1 will source this
// from ModelContract.Name instead.
func (t *sandboxTool) Name() string {
	return runtimeplan.ModelSafeToolName(firstNonEmpty(t.binding.RuntimeName, t.binding.Name))
}

// executorCapability is the Sandbox-local primitive that actually performs the
// action. It must not be conflated with the model-facing Tool name. For example:
//
//	model Tool: skill.pull
//	capability: git.fetch
//
// Legacy workspace/browser snapshots have no explicit capability and continue
// to use the catalog Tool id as a compatibility fallback.
func (t *sandboxTool) executorCapability() string {
	return firstNonEmpty(
		stringFromMap(t.binding.Execution, "executorCapability"),
		stringFromMap(t.binding.Execution, "executor_capability"),
		stringFromMap(t.binding.Runtime, "executorCapability"),
		stringFromMap(t.binding.Runtime, "executor_capability"),
		stringFromMap(t.binding.Runtime, "capability"),
		t.binding.Name,
	)
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
	capability := t.executorCapability()
	if strings.TrimSpace(capability) == "" {
		return nil, fmt.Errorf("sandbox tool %s has no executor capability", t.Name())
	}
	result, err := t.caller.CallTool(callCtx, t.sandboxID, sandboxclient.ToolCallRequest{
		Tool:          capability,
		Input:         mapStringInterface(input),
		RunID:         runID,
		TimeoutMillis: t.binding.TimeoutMillis,
		Metadata: map[string]interface{}{
			"runtimeType":        "sandbox",
			"toolId":             t.binding.Name,
			"modelToolName":      t.Name(),
			"canonicalToolName":  t.binding.Name,
			"executorCapability": capability,
			"toolVersion":        t.binding.Version,
			"toolRevision":       t.binding.Revision,
			"capabilities":       t.binding.Capabilities,
			"permissions":        t.binding.Permissions,
			"snapshotId":         t.snapshotID,
			"sessionId":          t.sessionID,
		},
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{"ok": true}, nil
	}
	if !result.OK {
		return nil, fmt.Errorf("sandbox tool %s (%s) failed: %v", t.Name(), capability, result.Error)
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
