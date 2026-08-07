package configurable

import (
	"context"
	"testing"
)

func TestRetiredAISphereToolReferencesAreUnreachable(t *testing.T) {
	for _, name := range retiredAISphereToolReferences {
		resolved, toolset, err := ResolveToolReference(context.Background(), name, nil)
		if err == nil {
			t.Fatalf("ResolveToolReference(%q) error = nil, want retired/not found", name)
		}
		if resolved != nil || toolset != nil {
			t.Fatalf("ResolveToolReference(%q) returned retired implementation: tool=%#v toolset=%#v", name, resolved, toolset)
		}
	}
}
