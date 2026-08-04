package runtimeexecutor

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/sandboxruntime"
	"google.golang.org/adk/internal/testutil"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
)

type fakeModel struct{}

func (fakeModel) Name() string { return "fake-model" }

func (fakeModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content:      genai.NewContentFromText("hello from go runner", genai.RoleModel),
			TurnComplete: true,
		}, nil)
	}
}

func TestExecutorRunsRuntimePlanThroughADKRunner(t *testing.T) {
	executor := &Executor{Model: fakeModel{}, SessionService: session.InMemoryService(), AutoCreateSession: true}
	events := 0
	for event, err := range executor.Run(context.Background(), RunRequest{
		Plan: &runtimeplan.RuntimePlan{
			SnapshotID: "snap-1",
			Agent:      runtimeplan.AgentSpec{ID: "agent-1", Name: "research_agent", Instruction: "Reply briefly."},
			Model:      runtimeplan.ModelSpec{Profile: "test", Model: "fake-model"},
		},
		AppName: "agent-1", UserID: "user-1", SessionID: "session-1", Message: "hi",
	}) {
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if event != nil {
			events++
		}
	}
	if events == 0 {
		t.Fatal("events = 0, want at least one")
	}
}

type recordingSandboxCaller struct {
	called bool
	tool   string
	input  map[string]interface{}
}

func (c *recordingSandboxCaller) CallTool(_ context.Context, sandboxID string, req sandboxclient.ToolCallRequest) (*sandboxclient.ToolCallResult, error) {
	if sandboxID != "sandbox-1" {
		return nil, fmt.Errorf("unexpected sandbox id %q", sandboxID)
	}
	c.called, c.tool, c.input = true, req.Tool, req.Input
	return &sandboxclient.ToolCallResult{OK: true, Tool: req.Tool, Result: map[string]interface{}{"value": "sandbox-result"}}, nil
}

func TestExecutorRunsModelToolLoopThroughSandboxAndPermissionGate(t *testing.T) {
	caller := &recordingSandboxCaller{}
	registry := toolruntime.New()
	if err := registry.Register("sandbox", sandboxruntime.Resolver{Caller: caller, SandboxID: "sandbox-1"}); err != nil {
		t.Fatalf("register sandbox resolver: %v", err)
	}
	model := &testutil.MockModel{Responses: []*genai.Content{
		genai.NewContentFromFunctionCall("workspace.read", map[string]any{"path": "README.md"}, genai.RoleModel),
		genai.NewContentFromText("closed-loop-complete", genai.RoleModel),
	}}
	executor := &Executor{Model: model, SessionService: session.InMemoryService(), ToolRegistry: registry, AutoCreateSession: true}
	plan := &runtimeplan.RuntimePlan{
		SnapshotID: "snap-closed-loop",
		Agent:      runtimeplan.AgentSpec{ID: "agent-1", Name: "closed_loop_agent", Instruction: "Use the authorized sandbox tool."},
		Tools:      []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox", ApprovalMode: "always", Approved: true}},
	}
	var sawToolResponse, sawFinalText bool
	for event, err := range executor.Run(t.Context(), RunRequest{
		Plan: plan, AppName: "agent-1", UserID: "user-1", SessionID: "session-1", Message: "read the file",
	}) {
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if event == nil || event.LLMResponse.Content == nil {
			continue
		}
		for _, part := range event.LLMResponse.Content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionResponse != nil && part.FunctionResponse.Name == "workspace.read" {
				sawToolResponse = true
			}
			if part.Text == "closed-loop-complete" {
				sawFinalText = true
			}
		}
	}
	if !caller.called || caller.tool != "workspace.read" || caller.input["path"] != "README.md" {
		t.Fatalf("sandbox call = called:%v tool:%q input:%v", caller.called, caller.tool, caller.input)
	}
	if !sawToolResponse || !sawFinalText {
		t.Fatalf("tool loop events missing: toolResponse=%v finalText=%v", sawToolResponse, sawFinalText)
	}
}
