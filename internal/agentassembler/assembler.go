// Package agentassembler turns RuntimePlan into ADK-Go agents.
package agentassembler

import (
	"fmt"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/internal/permissiongate"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

type Assembler struct {
	Model        model.LLM
	Tools        []tool.Tool
	Toolsets     []tool.Toolset
	ToolRegistry *toolruntime.Registry
}

func (a *Assembler) Assemble(plan *runtimeplan.RuntimePlan) (agent.Agent, error) {
	if plan == nil {
		return nil, fmt.Errorf("runtime plan is required")
	}
	if strings.TrimSpace(plan.Agent.ID) == "" {
		return nil, fmt.Errorf("runtime plan agent id is required")
	}
	if a.Model == nil {
		return nil, fmt.Errorf("runtime plan %s has no executable model adapter for profile %q", plan.SnapshotID, plan.Model.Profile)
	}
	name := firstNonEmpty(plan.Agent.Name, plan.Agent.ID)
	description := firstNonEmpty(plan.Agent.Description, "Hub-authorized AISphere agent")
	tools := append([]tool.Tool(nil), a.Tools...)
	toolsets := append([]tool.Toolset(nil), a.Toolsets...)
	if a.ToolRegistry != nil {
		resolvedTools, resolvedToolsets, err := a.ToolRegistry.ResolveAll(plan)
		if err != nil {
			return nil, err
		}
		tools = append(tools, resolvedTools...)
		toolsets = append(toolsets, resolvedToolsets...)
	}
	cfg := llmagent.Config{
		Name: name, Description: description, Model: a.Model,
		Instruction:         plan.Agent.Instruction,
		Tools:               tools,
		Toolsets:            toolsets,
		BeforeToolCallbacks: []llmagent.BeforeToolCallback{permissiongate.New(plan).BeforeToolCallback},
	}
	root, err := llmagent.New(cfg)
	if err != nil {
		return nil, err
	}
	return root, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
