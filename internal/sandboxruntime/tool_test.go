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

package sandboxruntime

import (
	"context"
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/toolinternal"
)

type fakeCaller struct {
	sandboxID string
	req       sandboxclient.ToolCallRequest
}

func (f *fakeCaller) CallTool(ctx context.Context, sandboxID string, req sandboxclient.ToolCallRequest) (*sandboxclient.ToolCallResult, error) {
	f.sandboxID = sandboxID
	f.req = req
	return &sandboxclient.ToolCallResult{OK: true, Tool: req.Tool, Result: map[string]interface{}{"content": "hello"}}, nil
}

func TestResolverCreatesExecutableSandboxTool(t *testing.T) {
	caller := &fakeCaller{}
	resolved, err := (Resolver{Caller: caller, SandboxID: "sandbox-1"}).ResolveTool(runtimeplan.ToolBinding{
		Name: "workspace.read", Version: "v1", Revision: "rev-1", RuntimeType: "sandbox",
		Runtime:     map[string]interface{}{"description": "Read a workspace file"},
		InputSchema: map[string]interface{}{"type": "object"},
	})
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	fn, ok := resolved.(toolinternal.FunctionTool)
	if !ok {
		t.Fatalf("resolved tool does not implement FunctionTool")
	}
	if got := fn.Declaration().Description; got != "Read a workspace file" {
		t.Fatalf("description = %q", got)
	}
	out, err := fn.Run(nil, map[string]any{"path": "README.md"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out["content"] != "hello" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if caller.sandboxID != "sandbox-1" || caller.req.Tool != "workspace.read" {
		t.Fatalf("unexpected call: sandbox=%s req=%+v", caller.sandboxID, caller.req)
	}
}

func TestSandboxToolSeparatesModelNameFromExecutorCapability(t *testing.T) {
	caller := &fakeCaller{}
	resolved, err := (Resolver{Caller: caller, SandboxID: "sandbox-1"}).ResolveTool(runtimeplan.ToolBinding{
		Name:        "skill.pull",
		RuntimeName: "skill_pull",
		RuntimeType: "sandbox",
		Runtime:     map[string]interface{}{"description": "Pull an authorized Skill draft"},
		Execution:   map[string]interface{}{"executorCapability": "git.fetch"},
		InputSchema: map[string]interface{}{"type": "object"},
	})
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	fn, ok := resolved.(toolinternal.FunctionTool)
	if !ok {
		t.Fatalf("resolved tool does not implement FunctionTool")
	}
	if got := fn.Declaration().Name; got != "skill_pull" {
		t.Fatalf("model Tool name = %q, want skill_pull", got)
	}
	if _, err := fn.Run(nil, map[string]any{"name": "demo"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if caller.req.Tool != "git.fetch" {
		t.Fatalf("executor Tool = %q, want git.fetch", caller.req.Tool)
	}
	if got := caller.req.Metadata["modelToolName"]; got != "skill_pull" {
		t.Fatalf("metadata modelToolName = %#v", got)
	}
	if got := caller.req.Metadata["executorCapability"]; got != "git.fetch" {
		t.Fatalf("metadata executorCapability = %#v", got)
	}
}
