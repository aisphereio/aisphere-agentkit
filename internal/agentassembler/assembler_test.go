package agentassembler

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

type fakeModel struct{}

func (fakeModel) Name() string { return "fake-model" }

func (fakeModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {}
}

func TestAssembleCreatesLLMAgentFromRuntimePlan(t *testing.T) {
	root, err := (&Assembler{Model: fakeModel{}}).Assemble(&runtimeplan.RuntimePlan{
		SnapshotID: "snap-1",
		Agent: runtimeplan.AgentSpec{
			ID: "agent-1", Name: "research_agent",
			Description: "Research helper", Instruction: "Use authorized tools only.",
		},
		Model: runtimeplan.ModelSpec{Profile: "coding-default", Model: "glm-5.2"},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if root.Name() != "research_agent" {
		t.Fatalf("Name() = %q, want research_agent", root.Name())
	}
}

func TestAssembleRequiresModelAdapter(t *testing.T) {
	_, err := (&Assembler{}).Assemble(&runtimeplan.RuntimePlan{SnapshotID: "snap-1", Agent: runtimeplan.AgentSpec{ID: "agent-1"}})
	if err == nil {
		t.Fatal("Assemble() error = nil, want error")
	}
}

type fakeTool struct{ name string }

func (f fakeTool) Name() string      { return f.name }
func (fakeTool) Description() string { return "fake" }
func (fakeTool) IsLongRunning() bool { return false }

func TestAssembleResolvesToolsFromRegistry(t *testing.T) {
	registry := toolruntime.New()
	if err := registry.Register("sandbox", toolruntime.ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	_, err := (&Assembler{Model: fakeModel{}, ToolRegistry: registry}).Assemble(&runtimeplan.RuntimePlan{
		SnapshotID: "snap-1",
		Agent:      runtimeplan.AgentSpec{ID: "agent-1", Name: "research_agent"},
		Tools:      []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox", ApprovalMode: "always", Approved: true}},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
}
