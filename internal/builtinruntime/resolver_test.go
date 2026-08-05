package builtinruntime

import (
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
)

func TestResolverUsesExistingBuiltinFactoryRegistry(t *testing.T) {
	resolved, err := (Resolver{}).ResolveTool(runtimeplan.ToolBinding{RuntimeName: "exit_loop"})
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	if resolved == nil || resolved.Name() != "exit_loop" {
		t.Fatalf("unexpected builtin tool: %#v", resolved)
	}
}
