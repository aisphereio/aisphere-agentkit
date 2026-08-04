package toolruntime

import (
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/tool"
)

type fakeTool struct{ name string }

func (f fakeTool) Name() string      { return f.name }
func (fakeTool) Description() string { return "fake" }
func (fakeTool) IsLongRunning() bool { return false }

func TestRegistryResolvesPlanToolsByRuntimeType(t *testing.T) {
	registry := New()
	if err := registry.Register("sandbox", ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	tools, err := registry.Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox"}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "workspace.read" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestRegistryFailsClosedForUnknownRuntimeType(t *testing.T) {
	_, err := New().Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox"}}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
}

type fakeToolset struct{ name string }

func (f fakeToolset) Name() string { return f.name }
func (f fakeToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return []tool.Tool{fakeTool{name: f.name + ".tool"}}, nil
}

func TestRegistryResolvesToolsetsByRuntimeType(t *testing.T) {
	registry := New()
	if err := registry.RegisterToolset("mcp", ToolsetResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Toolset, error) {
		return fakeToolset{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("RegisterToolset() error = %v", err)
	}
	tools, toolsets, err := registry.ResolveAll(&runtimeplan.RuntimePlan{
		Tools: []runtimeplan.ToolBinding{{Name: "novel_assets", RuntimeType: "mcp"}},
	})
	if err != nil {
		t.Fatalf("ResolveAll() error = %v", err)
	}
	if len(tools) != 0 || len(toolsets) != 1 || toolsets[0].Name() != "novel_assets" {
		t.Fatalf("unexpected resolved tools/toolsets: %d/%d", len(tools), len(toolsets))
	}
}
