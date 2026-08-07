// Package builtinruntime adapts Runtime-owned builtin implementations to Hub
// RuntimePlans. Builtin executable code is compiled into the Runtime binary;
// Hub only selects a mirrored immutable descriptor/version.
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
	Config   *runtimeconfig.Config
	Registry *Registry
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

	// Tool V1: when a Runtime-owned registry is configured, the Hub binding is
	// only a selector. No executable code is downloaded from Hub. Resolution is
	// local and fails closed when the requested implementation is unavailable.
	if r.Registry != nil {
		resolved, _, err := r.Registry.Resolve(ctx, name, binding.ImplementationVersion, toolArgs(binding))
		return resolved, err
	}

	// Transitional compatibility for the current AgentKit configurable factory
	// registry. New production Builtins must be registered in Registry instead of
	// adding more implicit configurable aliases here.
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
