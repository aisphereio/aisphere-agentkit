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
