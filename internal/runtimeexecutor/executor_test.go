package runtimeexecutor

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/sandboxruntime"
	"google.golang.org/adk/internal/skillruntime"
	"google.golang.org/adk/internal/testutil"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
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
		genai.NewContentFromFunctionCall("workspace_read_7719d3f4", map[string]any{"path": "README.md"}, genai.RoleModel),
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
			if part.FunctionResponse != nil && part.FunctionResponse.Name == "workspace_read_7719d3f4" {
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

func TestExecutorLoadsAuthorizedSkillThroughPermissionGate(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "release-notes")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: release-notes\ndescription: Summarize a release\n---\nFollow the release checklist."), 0o644); err != nil {
		t.Fatal(err)
	}
	bindings := []runtimeplan.SkillBinding{{Name: "release-notes", Version: "v1.2.0"}}
	set, err := skillruntime.NewToolset(t.Context(), root, bindings)
	if err != nil {
		t.Fatalf("NewToolset() error = %v", err)
	}
	model := &testutil.MockModel{Responses: []*genai.Content{
		genai.NewContentFromFunctionCall("load_skill", map[string]any{"name": "release-notes"}, genai.RoleModel),
		genai.NewContentFromText("skill-context-loaded", genai.RoleModel),
	}}
	executor := &Executor{
		Model: model, SessionService: session.InMemoryService(), Toolsets: []tool.Toolset{set}, AutoCreateSession: true,
	}
	plan := &runtimeplan.RuntimePlan{
		SnapshotID: "snap-skill-loop",
		Agent:      runtimeplan.AgentSpec{ID: "agent-1", Name: "skill_agent", Instruction: "Load the authorized Skill."},
		Skills:     bindings,
	}
	var sawSkillResponse, sawFinalText bool
	for event, runErr := range executor.Run(t.Context(), RunRequest{
		Plan: plan, AppName: "agent-1", UserID: "user-1", SessionID: "session-1", Message: "summarize the release",
	}) {
		if runErr != nil {
			t.Fatalf("Run() error = %v", runErr)
		}
		if eventErr := EventError(event); eventErr != nil {
			t.Fatalf("EventError() = %v", eventErr)
		}
		if event == nil || event.LLMResponse.Content == nil {
			continue
		}
		for _, part := range event.LLMResponse.Content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionResponse != nil && part.FunctionResponse.Name == "load_skill" {
				sawSkillResponse = true
				if got := fmt.Sprint(part.FunctionResponse.Response["instructions"]); got != "Follow the release checklist." {
					t.Fatalf("instructions = %q", got)
				}
			}
			if part.Text == "skill-context-loaded" {
				sawFinalText = true
			}
		}
	}
	if !sawSkillResponse || !sawFinalText {
		t.Fatalf("skill tool loop events missing: toolResponse=%v finalText=%v", sawSkillResponse, sawFinalText)
	}
}

func TestRuntimeExecutionErrorDetectsDeniedToolAndSkillLoadFailures(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		response map[string]any
	}{
		{
			name:     "permission gate denial",
			toolName: "workspace.write",
			response: map[string]any{"error": "tool is not allowed by runtime plan: workspace.write"},
		},
		{
			name:     "skill load failure",
			toolName: "load_skill",
			response: map[string]any{"error": "load instructions for skill release-notes: file missing"},
		},
		{
			name:     "sandbox tool execution failure",
			toolName: "workspace_write_8ea1c313",
			response: map[string]any{"error": "sandbox tool endpoint is unavailable"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			event := &session.Event{LLMResponse: model.LLMResponse{Content: &genai.Content{
				Role:  genai.RoleUser,
				Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: tc.toolName, Response: tc.response}}},
			}}}
			if err := EventError(event); err == nil {
				t.Fatal("EventError() = nil, want failure")
			} else if tc.name == "sandbox tool execution failure" && FailureCode(err) != "TOOL_EXECUTION_FAILED" {
				t.Fatalf("FailureCode() = %q, want TOOL_EXECUTION_FAILED", FailureCode(err))
			}
		})
	}
}

func TestRuntimeExecutionErrorAllowsSuccessfulSkillLoad(t *testing.T) {
	event := &session.Event{LLMResponse: model.LLMResponse{Content: &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			Name: "load_skill", Response: map[string]any{"instructions": "Follow the release checklist."},
		}}},
	}}}
	if err := EventError(event); err != nil {
		t.Fatalf("EventError() = %v, want nil", err)
	}
}
