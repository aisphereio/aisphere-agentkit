package runtimeplan

import (
	"testing"

	"google.golang.org/adk/internal/aihubruntime"
)

func TestFromSnapshotBuildsAuthorizedRuntimePlan(t *testing.T) {
	plan, err := FromSnapshot(&aihubruntime.AgentSnapshot{
		SnapshotID: "snap-1", RuntimeID: "runtime-1", SessionID: "session-1",
		AgentID: "agent-1", AgentVersion: "v1", AgentRevision: "rev-1",
		Policy: "principal_passthrough_iam_enforced",
		Definition: aihubruntime.AgentDefinition{
			EntryPoint: "root_agent.yaml",
			Files: map[string]string{
				"root_agent.yaml": "name: research_agent\ndescription: Research helper\ninstruction: Use only authorized tools.\n",
			},
			Model: aihubruntime.ModelSpec{Profile: "fallback-profile", Model: "fallback-model"},
		},
		Model:   aihubruntime.ModelSpec{Profile: "coding-default", Model: "glm-5.2"},
		Sandbox: aihubruntime.SandboxSpec{Profile: "agent-default", NetworkMode: "restricted"},
		Skills:  []aihubruntime.SkillSnapshotItem{{Name: "release-notes", Version: "builtin", Object: "aisphere://builtin-skills/release-notes"}},
		Tools: []aihubruntime.ToolSnapshotItem{{
			Name: "workspace.read", Version: "v1", Revision: "tool-rev", Status: "active",
			Runtime:     map[string]interface{}{"type": "sandbox"},
			Execution:   map[string]interface{}{"capabilities": []any{"sandbox:read"}},
			InputSchema: map[string]interface{}{"type": "object"},
		}},
		Authorization: map[string]any{
			"principalSubject": "user:user-1", "iamEnforcement": "resource_service",
			"requiresApproval": false, "approvalConfirmed": true,
			"tools": []any{map[string]any{
				"tool": "workspace.read", "version": "v1", "approvalMode": "always", "approved": true,
				"permissions": []any{map[string]any{"resourceType": "sandbox", "permission": "read", "enforcement": "iam_at_resource_service"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("FromSnapshot() error = %v", err)
	}
	if plan.Agent.Name != "research_agent" || plan.Agent.Instruction != "Use only authorized tools." {
		t.Fatalf("unexpected agent spec: %+v", plan.Agent)
	}
	if plan.Model.Profile != "coding-default" || plan.Model.Model != "glm-5.2" {
		t.Fatalf("unexpected model spec: %+v", plan.Model)
	}
	if len(plan.Skills) != 1 || plan.Skills[0].Source != "builtin" {
		t.Fatalf("unexpected skills: %+v", plan.Skills)
	}
	if len(plan.Tools) != 1 || plan.Tools[0].ApprovalMode != "always" || !plan.Tools[0].Approved {
		t.Fatalf("unexpected tools: %+v", plan.Tools)
	}
	if got := plan.Authorization.Tools[0].Permissions[0].Permission; got != "read" {
		t.Fatalf("permission = %q, want read", got)
	}
}

func TestFromSnapshotRequiresPinnedEntrypoint(t *testing.T) {
	_, err := FromSnapshot(&aihubruntime.AgentSnapshot{
		SnapshotID: "snap-1", AgentID: "agent-1",
		Definition: aihubruntime.AgentDefinition{EntryPoint: "root_agent.yaml", Files: map[string]string{"other.yaml": "name: x\n"}},
	})
	if err == nil {
		t.Fatal("FromSnapshot() error = nil, want error")
	}
}
