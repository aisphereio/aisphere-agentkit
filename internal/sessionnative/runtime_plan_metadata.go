// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sessionnative

import (
	"strings"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/runtimeplan"
)

func runtimePlanMetadata(plan *runtimeplan.RuntimePlan) map[string]any {
	if plan == nil {
		return nil
	}
	tools := make([]map[string]any, 0, len(plan.Tools))
	for _, item := range plan.Tools {
		tools = append(tools, map[string]any{
			"name":             item.Name,
			"runtimeName":      item.RuntimeName,
			"version":          item.Version,
			"revision":         item.Revision,
			"runtimeType":      item.RuntimeType,
			"approvalMode":     item.ApprovalMode,
			"approved":         item.Approved,
			"requiresApproval": item.RequiresApproval,
			"capabilities":     item.Capabilities,
			"permissions":      item.Permissions,
		})
	}
	skills := make([]map[string]any, 0, len(plan.Skills))
	for _, item := range plan.Skills {
		skills = append(skills, map[string]any{
			"name": item.Name, "version": item.Version, "revision": item.Revision,
			"source": item.Source, "object": item.Object, "mountPath": item.MountPath,
		})
	}
	return map[string]any{
		"snapshotId":  plan.SnapshotID,
		"runtimeId":   plan.RuntimeID,
		"sessionId":   plan.SessionID,
		"policy":      plan.Policy,
		"generatedAt": plan.GeneratedAt,
		"agent": map[string]any{
			"id": plan.Agent.ID, "version": plan.Agent.Version, "revision": plan.Agent.Revision,
			"name": plan.Agent.Name, "description": plan.Agent.Description, "entryPoint": plan.Agent.EntryPoint,
		},
		"model":         plan.Model,
		"sandbox":       plan.Sandbox,
		"skills":        skills,
		"tools":         tools,
		"mcpServers":    plan.MCPServers,
		"authorization": plan.Authorization,
	}
}

func allowedTools(plan *runtimeplan.RuntimePlan, snapshot *aihubruntime.AgentSnapshot) []string {
	if plan != nil {
		out := make([]string, 0, len(plan.Tools))
		for _, item := range plan.Tools {
			if strings.TrimSpace(item.Name) != "" {
				out = append(out, strings.TrimSpace(item.Name))
			}
		}
		return out
	}
	out := make([]string, 0, len(snapshot.Tools))
	for _, item := range snapshot.Tools {
		if strings.TrimSpace(item.Name) != "" {
			out = append(out, strings.TrimSpace(item.Name))
		}
	}
	return out
}

func toolSchemas(plan *runtimeplan.RuntimePlan, snapshot *aihubruntime.AgentSnapshot) []map[string]interface{} {
	if plan != nil {
		out := make([]map[string]interface{}, 0, len(plan.Tools))
		for _, item := range plan.Tools {
			if strings.TrimSpace(item.Name) == "" {
				continue
			}
			out = append(out, map[string]interface{}{
				"name":        item.Name,
				"runtimeName": item.RuntimeName,
				"description": toolDescriptionFromPlan(item),
				"inputSchema": item.InputSchema,
				"version":     item.Version,
				"revision":    item.Revision,
			})
		}
		return out
	}
	out := make([]map[string]interface{}, 0, len(snapshot.Tools))
	for _, item := range snapshot.Tools {
		if strings.TrimSpace(item.Name) != "" {
			out = append(out, map[string]interface{}{
				"name":        item.Name,
				"description": toolDescription(item),
				"inputSchema": item.InputSchema,
				"version":     item.Version,
				"revision":    item.Revision,
			})
		}
	}
	return out
}

func skillRefs(plan *runtimeplan.RuntimePlan, snapshot *aihubruntime.AgentSnapshot) []map[string]string {
	if plan != nil {
		out := make([]map[string]string, 0, len(plan.Skills))
		for _, item := range plan.Skills {
			if strings.TrimSpace(item.Name) != "" {
				out = append(out, map[string]string{"name": item.Name, "version": item.Version})
			}
		}
		return out
	}
	out := make([]map[string]string, 0, len(snapshot.Skills))
	for _, item := range snapshot.Skills {
		if strings.TrimSpace(item.Name) != "" {
			out = append(out, map[string]string{"name": item.Name, "version": item.Version})
		}
	}
	return out
}

func toolDescriptionFromPlan(item runtimeplan.ToolBinding) string {
	if item.Runtime != nil {
		if value, ok := item.Runtime["description"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	if item.Metadata != nil {
		if value, ok := item.Metadata["description"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return "Authorized Agent tool"
}
