package sessionnative

import "testing"

func TestCleanDefinitionPath(t *testing.T) {
	for _, raw := range []string{"root_agent.yaml", "nested/agent.yaml", `nested\agent.yaml`} {
		if _, err := cleanDefinitionPath(raw); err != nil {
			t.Fatalf("cleanDefinitionPath(%q) unexpected error: %v", raw, err)
		}
	}
	for _, raw := range []string{"", ".", "..", "../secret", "/abs/root_agent.yaml", "nested/../../secret"} {
		if _, err := cleanDefinitionPath(raw); err == nil {
			t.Fatalf("cleanDefinitionPath(%q) expected error", raw)
		}
	}
}

func TestFilesystemName(t *testing.T) {
	if got, want := filesystemName("research.agent/v1"), "research.agent_v1"; got != want {
		t.Fatalf("filesystemName() = %q, want %q", got, want)
	}
	if got := filesystemName("..."); got != "agent" {
		t.Fatalf("filesystemName empty fallback = %q, want agent", got)
	}
}
