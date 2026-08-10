package builtinruntime

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/tool"
)

type fakeTool struct{ name string }

func (f fakeTool) Name() string        { return f.name }
func (f fakeTool) Description() string { return "fake" }
func (f fakeTool) IsLongRunning() bool { return false }

func TestRegistryResolvesExactBuiltinImplementation(t *testing.T) {
	registry := NewRegistry()
	descriptor := Descriptor{
		ID:                    "memory.search",
		ImplementationVersion: "1",
		Model: ModelContract{
			Name:        "memory_search",
			Description: "Search memory",
			InputSchema: map[string]any{"type": "object"},
		},
	}
	if err := registry.Register(descriptor, func(context.Context, map[string]any) (tool.Tool, error) {
		return fakeTool{name: "memory_search"}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !registry.Has("memory.search", "1") || !registry.Has("memory.search", "") {
		t.Fatal("registered builtin should be available by exact and unique-version lookup")
	}
	if registry.Has("memory.search", "2") {
		t.Fatal("unregistered implementation version must not be reported available")
	}
	resolved, got, err := registry.Resolve(context.Background(), "memory.search", "1", nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Name() != "memory_search" {
		t.Fatalf("resolved name = %q", resolved.Name())
	}
	if got.Digest == "" {
		t.Fatal("descriptor digest is empty")
	}
}

func TestRegistryRejectsUnavailableVersion(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Descriptor{
		ID: "artifact.read", ImplementationVersion: "1", Model: ModelContract{Name: "artifact_read"},
	}, func(context.Context, map[string]any) (tool.Tool, error) {
		return fakeTool{name: "artifact_read"}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	_, _, err := registry.Resolve(context.Background(), "artifact.read", "2", nil)
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("Resolve() error = %v, want unavailable implementation", err)
	}
}

func TestRegistryRequiresVersionWhenMultipleImplementationsExist(t *testing.T) {
	registry := NewRegistry()
	for _, version := range []string{"1", "2"} {
		if err := registry.Register(Descriptor{
			ID: "knowledge.search", ImplementationVersion: version, Model: ModelContract{Name: "knowledge_search"},
		}, func(context.Context, map[string]any) (tool.Tool, error) {
			return fakeTool{name: "knowledge_search"}, nil
		}); err != nil {
			t.Fatalf("Register(%s) error = %v", version, err)
		}
	}
	if registry.Has("knowledge.search", "") {
		t.Fatal("blank implementation version must be ambiguous when multiple versions are registered")
	}
	_, _, err := registry.Resolve(context.Background(), "knowledge.search", "", nil)
	if err == nil || !strings.Contains(err.Error(), "exact version is required") {
		t.Fatalf("Resolve() error = %v, want exact-version requirement", err)
	}
}

func TestManifestIsDescriptorOnlyAndDeterministic(t *testing.T) {
	registry := NewRegistry()
	for _, descriptor := range []Descriptor{
		{ID: "project.info", ImplementationVersion: "1", Model: ModelContract{Name: "project_info"}},
		{ID: "artifact.read", ImplementationVersion: "2", Model: ModelContract{Name: "artifact_read"}},
	} {
		if err := registry.Register(descriptor, func(context.Context, map[string]any) (tool.Tool, error) {
			return fakeTool{name: descriptor.Model.Name}, nil
		}); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}
	manifest := registry.Manifest("runtime-test")
	if len(manifest.Builtins) != 2 {
		t.Fatalf("manifest builtins = %d", len(manifest.Builtins))
	}
	if manifest.Builtins[0].ID != "artifact.read" || manifest.Builtins[1].ID != "project.info" {
		t.Fatalf("manifest order = %#v", manifest.Builtins)
	}
	if manifest.Builtins[0].Digest == "" || manifest.Builtins[1].Digest == "" {
		t.Fatal("manifest descriptor digest is empty")
	}
}
