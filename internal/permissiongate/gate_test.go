package permissiongate

import (
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
)

func TestGateAllowsSnapshotTool(t *testing.T) {
	gate := New(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read", ApprovalMode: "always", Approved: true}}})
	if decision := gate.Check("workspace.read"); !decision.Allowed {
		t.Fatalf("Allowed = false, reason = %s", decision.Reason)
	}
}

func TestGateDeniesToolOutsideSnapshot(t *testing.T) {
	gate := New(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read"}}})
	decision := gate.Check("workspace.write")
	if decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if decision.Reason == "" {
		t.Fatal("Reason is empty")
	}
}

func TestGateRequiresPerRunApproval(t *testing.T) {
	gate := New(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "shell.exec", ApprovalMode: "per_run", Approved: false}}})
	decision := gate.Check("shell.exec")
	if !decision.ApprovalRequired || decision.Allowed {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestGateFailsClosedWhenAuthorizationModeIsMissing(t *testing.T) {
	decision := New(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read"}}}).Check("workspace.read")
	if decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestGateAllowsInternalSkillContextToolsWhenSnapshotContainsSkills(t *testing.T) {
	gate := New(&runtimeplan.RuntimePlan{Skills: []runtimeplan.SkillBinding{{Name: "release-notes", Version: "v1.2.0"}}})
	for _, name := range []string{"list_skills", "load_skill", "load_skill_resource"} {
		if decision := gate.Check(name); !decision.Allowed {
			t.Fatalf("Check(%q) Allowed = false, reason = %s", name, decision.Reason)
		}
	}
}

func TestGateDeniesInternalSkillContextToolsWithoutSnapshotSkills(t *testing.T) {
	decision := New(&runtimeplan.RuntimePlan{}).Check("load_skill")
	if decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}
