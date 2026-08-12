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
		Skills: []aihubruntime.SkillSnapshotItem{{
			Name: "release-notes", Version: "v1.2.0", Source: "catalog", Object: "aihub:skill:release-notes",
			CommitSHA: "commit-1", TreeSHA: "tree-1", ManifestSHA256: "manifest-1", ViaSkillSet: "release-workflow",
			SHA256: "package-1", MD5: "package-md5", Size: 42, DownloadURL: "/signed/package",
		}},
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
	if len(plan.Skills) != 1 || plan.Skills[0].Source != "catalog" {
		t.Fatalf("unexpected skills: %+v", plan.Skills)
	}
	skill := plan.Skills[0]
	if skill.CommitSHA != "commit-1" || skill.TreeSHA != "tree-1" || skill.ManifestSHA256 != "manifest-1" || skill.ViaSkillSet != "release-workflow" || skill.SHA256 != "package-1" || skill.MD5 != "package-md5" || skill.Size != 42 {
		t.Fatalf("skill provenance was lost: %+v", skill)
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

func TestFromSnapshotNormalizesLegacyGoMarshaledToolDefinition(t *testing.T) {
	plan, err := FromSnapshot(&aihubruntime.AgentSnapshot{
		SnapshotID: "snap-legacy-tool", AgentID: "agent-1",
		Definition: aihubruntime.AgentDefinition{
			EntryPoint: "root_agent.yaml",
			Files:      map[string]string{"root_agent.yaml": "name: agent-1\n"},
		},
		Tools: []aihubruntime.ToolSnapshotItem{{
			Name: "workspace.write", Version: "builtin-v1",
			Runtime: map[string]interface{}{
				"Type": "builtin", "Name": "workspace.write", "Description": "Write a workspace file",
			},
			Execution: map[string]interface{}{
				"Placement": "sandbox", "Capabilities": []any{"sandbox:write"},
			},
		}},
		Authorization: map[string]any{"tools": []any{map[string]any{
			"tool": "workspace.write", "approvalMode": "always", "approved": true,
		}}},
	})
	if err != nil {
		t.Fatalf("FromSnapshot() error = %v", err)
	}
	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %+v", plan.Tools)
	}
	got := plan.Tools[0]
	if got.RuntimeType != "builtin" || got.RuntimeName != "workspace.write" || got.Execution["placement"] != "sandbox" {
		t.Fatalf("legacy ToolDefinition was not normalized: %+v", got)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != "sandbox:write" {
		t.Fatalf("capabilities = %+v", got.Capabilities)
	}
}

func TestModelSafeToolNameAdaptsCatalogNamespace(t *testing.T) {
	tests := map[string]string{
		"workspace.write": "workspace_write_8ea1c313",
		"load_skill":      "load_skill",
		"skill/pull":      "skill_pull_7a0ca7fa",
	}
	for input, want := range tests {
		if got := ModelSafeToolName(input); got != want {
			t.Fatalf("ModelSafeToolName(%q) = %q, want %q", input, got, want)
		}
	}
}
