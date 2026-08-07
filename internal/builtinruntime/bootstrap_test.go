package builtinruntime

import (
	"context"
	"testing"
)

func TestDefaultRegistryContainsOnlyExplicitBuiltinV1Batch(t *testing.T) {
	want := map[string]bool{
		"save_artifact":     true,
		"load_artifacts":    true,
		"list_artifacts":    true,
		"delete_artifact":   true,
		"load_memory":       true,
		"get_user_choice":   true,
		"request_user_form": true,
	}
	manifest := DefaultRegistry().Manifest("test")
	for _, descriptor := range manifest.Builtins {
		if !want[descriptor.ID] {
			t.Fatalf("unexpected Tool V1 builtin in default registry: %s", descriptor.ID)
		}
		delete(want, descriptor.ID)
		if descriptor.ImplementationVersion != builtinImplementationVersion {
			t.Fatalf("builtin %s implementation version = %q", descriptor.ID, descriptor.ImplementationVersion)
		}
		if descriptor.Digest == "" {
			t.Fatalf("builtin %s has empty descriptor digest", descriptor.ID)
		}
		if descriptor.Model.Name == "" {
			t.Fatalf("builtin %s has empty model name", descriptor.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing Tool V1 builtins: %#v", want)
	}
}

func TestDefaultRegistryResolvesPinnedBuiltinLocally(t *testing.T) {
	resolved, descriptor, err := DefaultRegistry().Resolve(context.Background(), "load_memory", builtinImplementationVersion, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved == nil || resolved.Name() != "load_memory" {
		t.Fatalf("unexpected resolved tool: %#v", resolved)
	}
	if descriptor.ID != "load_memory" || descriptor.ImplementationVersion != builtinImplementationVersion {
		t.Fatalf("unexpected descriptor: %#v", descriptor)
	}
}
