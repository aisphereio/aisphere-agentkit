package skillruntime

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
)

func TestNewToolsetLoadsOnlySnapshotSkills(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "demo", "SKILL.md"), []byte("---\nname: demo\ndescription: Demo\n---\nUse demo."), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := NewToolset(t.Context(), root, []runtimeplan.SkillBinding{{Name: "demo", Version: "v1"}})
	if err != nil {
		t.Fatalf("NewToolset() error = %v", err)
	}
	if set == nil || set.Name() == "" {
		t.Fatalf("unexpected toolset: %#v", set)
	}
}
