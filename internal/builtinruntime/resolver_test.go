package builtinruntime

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/tool"
)

func TestResolverUsesExistingBuiltinFactoryRegistry(t *testing.T) {
	resolved, err := (Resolver{Registry: NewRegistry()}).ResolveTool(runtimeplan.ToolBinding{RuntimeName: "exit_loop"})
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	if resolved == nil || resolved.Name() != "exit_loop" {
		t.Fatalf("unexpected builtin tool: %#v", resolved)
	}
}

func TestResolverPrefersCodeOwnedRegistry(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Descriptor{
		ID:                    "memory.search",
		ImplementationVersion: "1",
		Model:                 ModelContract{Name: "memory_search"},
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return fakeTool{name: "memory_search"}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	resolved, err := (Resolver{Registry: registry}).ResolveTool(runtimeplan.ToolBinding{
		Name:        "memory.search",
		RuntimeName: "memory.search",
		Metadata:    map[string]interface{}{"implementationVersion": "1"},
	})
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	if resolved == nil || resolved.Name() != "memory_search" {
		t.Fatalf("unexpected builtin tool: %#v", resolved)
	}
}

func TestResolverPinnedV1DoesNotFallBackToLegacyFactory(t *testing.T) {
	_, err := (Resolver{Registry: NewRegistry()}).ResolveTool(runtimeplan.ToolBinding{
		RuntimeName: "exit_loop",
		Metadata:    map[string]interface{}{"implementationVersion": "1"},
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("ResolveTool() error = %v, want pinned implementation unavailable", err)
	}
}

func TestResolverUsesDefaultRegistryForMigratedPinnedBuiltin(t *testing.T) {
	resolved, err := (Resolver{}).ResolveTool(runtimeplan.ToolBinding{
		Name:        "load_memory",
		RuntimeName: "load_memory",
		RuntimeType: "builtin",
		Metadata:    map[string]interface{}{"implementationVersion": builtinImplementationVersion},
	})
	if err != nil {
		t.Fatalf("ResolveTool() error = %v", err)
	}
	if resolved == nil || resolved.Name() != "load_memory" {
		t.Fatalf("unexpected migrated builtin tool: %#v", resolved)
	}
}
