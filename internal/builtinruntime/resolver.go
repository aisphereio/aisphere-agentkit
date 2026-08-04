// Package builtinruntime adapts AgentKit's existing configurable tool
// factories to Hub RuntimePlans. It keeps the factory registry as the single
// implementation source for built-in tools.
package builtinruntime

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/internal/configurable"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/tool"
)

type Resolver struct {
	Config *runtimeconfig.Config
}

func (r Resolver) ResolveTool(binding runtimeplan.ToolBinding) (tool.Tool, error) {
	name := firstNonEmpty(binding.RuntimeName, binding.Name)
	if name == "" {
		return nil, fmt.Errorf("builtin tool name is required")
	}
	ctx := context.Background()
	if r.Config != nil {
		ctx = runtimeconfig.WithConfig(ctx, r.Config)
	}
	resolved, toolset, err := configurable.ResolveToolReference(ctx, name, toolArgs(binding))
	if err != nil {
		return nil, err
	}
	if toolset != nil {
		return nil, fmt.Errorf("builtin reference %q resolves to a toolset, not a tool", name)
	}
	if resolved == nil {
		return nil, fmt.Errorf("builtin resolver returned nil tool for %q", name)
	}
	return resolved, nil
}

func toolArgs(binding runtimeplan.ToolBinding) map[string]any {
	args := map[string]any{}
	if raw, ok := binding.Runtime["config"].(map[string]any); ok {
		for key, value := range raw {
			args[key] = value
		}
	}
	if raw, ok := binding.Metadata["args"].(map[string]any); ok {
		for key, value := range raw {
			args[key] = value
		}
	}
	return args
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
