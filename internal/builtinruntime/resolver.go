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
	implementationVersion := firstNonEmpty(
		stringValue(binding.Runtime["implementationVersion"]),
		stringValue(binding.Runtime["implementation_version"]),
		stringValue(binding.Metadata["implementationVersion"]),
		stringValue(binding.Metadata["implementation_version"]),
	)

	registry := r.Registry
	if registry == nil {
		registry = DefaultRegistry()
	}

	// A V1 binding that pins an implementation version must resolve exactly from
	// Runtime-local code. Missing code is a preparation failure; never downgrade
	// to the legacy configurable registry or silently substitute another version.
	if implementationVersion != "" {
		if !registry.Has(name, implementationVersion) {
			return nil, fmt.Errorf("builtin implementation %s@%s is not available in this Runtime", name, implementationVersion)
		}
		resolved, _, err := registry.Resolve(ctx, name, implementationVersion, toolArgs(binding))
		return resolved, err
	}

	// During migration, old Hub snapshots do not carry implementationVersion.
	// If exactly one code-owned implementation exists, prefer it. Otherwise only
	// pre-V1 bindings may fall through to the old configurable factory registry.
	if registry.Has(name, "") {
		resolved, _, err := registry.Resolve(ctx, name, "", toolArgs(binding))
		return resolved, err
	}

	// Transitional compatibility for pre-V1 Agent/Tool snapshots. New production
	// Builtins must be registered in DefaultRegistry instead of adding aliases to
	// the configurable factory registry.
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

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
