// Package toolruntime maps Hub tool bindings to executable ADK tool adapters.
// It is the single seam where sandbox tools, MCP tools, and internal Runtime
// tools become visible to the ADK-Go agent.
package toolruntime

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/tool"
)

type Resolver interface {
	ResolveTool(binding runtimeplan.ToolBinding) (tool.Tool, error)
}

type ResolverFunc func(runtimeplan.ToolBinding) (tool.Tool, error)

func (f ResolverFunc) ResolveTool(binding runtimeplan.ToolBinding) (tool.Tool, error) {
	return f(binding)
}

type ToolsetResolver interface {
	ResolveToolset(binding runtimeplan.ToolBinding) (tool.Toolset, error)
}

type ToolsetResolverFunc func(runtimeplan.ToolBinding) (tool.Toolset, error)

func (f ToolsetResolverFunc) ResolveToolset(binding runtimeplan.ToolBinding) (tool.Toolset, error) {
	return f(binding)
}

type Registry struct {
	resolvers        map[string]Resolver
	toolsetResolvers map[string]ToolsetResolver
}

func New() *Registry {
	return &Registry{resolvers: map[string]Resolver{}, toolsetResolvers: map[string]ToolsetResolver{}}
}

func (r *Registry) Register(runtimeType string, resolver Resolver) error {
	runtimeType = normalizeRuntimeType(runtimeType)
	if runtimeType == "" {
		return fmt.Errorf("runtime type is required")
	}
	if resolver == nil {
		return fmt.Errorf("resolver is required for runtime type %q", runtimeType)
	}
	if r.resolvers == nil {
		r.resolvers = map[string]Resolver{}
	}
	r.resolvers[runtimeType] = resolver
	return nil
}

func (r *Registry) RegisterToolset(runtimeType string, resolver ToolsetResolver) error {
	runtimeType = normalizeRuntimeType(runtimeType)
	if runtimeType == "" {
		return fmt.Errorf("runtime type is required")
	}
	if resolver == nil {
		return fmt.Errorf("toolset resolver is required for runtime type %q", runtimeType)
	}
	if r.toolsetResolvers == nil {
		r.toolsetResolvers = map[string]ToolsetResolver{}
	}
	r.toolsetResolvers[runtimeType] = resolver
	return nil
}

func (r *Registry) Resolve(plan *runtimeplan.RuntimePlan) ([]tool.Tool, error) {
	tools, _, err := r.ResolveAll(plan)
	return tools, err
}

// ResolveAll resolves both individual tools and lazy toolsets. Toolsets are
// kept as tool.Toolset so ADK can discover remote tools at request time (MCP
// sessions are intentionally lazy).
func (r *Registry) ResolveAll(plan *runtimeplan.RuntimePlan) ([]tool.Tool, []tool.Toolset, error) {
	if plan == nil {
		return nil, nil, fmt.Errorf("runtime plan is required")
	}
	tools := make([]tool.Tool, 0, len(plan.Tools))
	toolsets := make([]tool.Toolset, 0)
	for _, binding := range plan.Tools {
		if strings.TrimSpace(binding.Name) == "" {
			continue
		}
		runtimeType := normalizeRuntimeType(binding.RuntimeType)
		if runtimeType == "" {
			runtimeType = "internal"
		}
		if resolver := r.resolvers[runtimeType]; resolver != nil {
			resolved, err := resolver.ResolveTool(binding)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve tool %s: %w", binding.Name, err)
			}
			if resolved == nil {
				return nil, nil, fmt.Errorf("resolver returned nil tool for %q", binding.Name)
			}
			tools = append(tools, resolved)
			continue
		}
		if resolver := r.toolsetResolvers[runtimeType]; resolver != nil {
			resolved, err := resolver.ResolveToolset(binding)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve toolset %s: %w", binding.Name, err)
			}
			if resolved == nil {
				return nil, nil, fmt.Errorf("resolver returned nil toolset for %q", binding.Name)
			}
			toolsets = append(toolsets, resolved)
			continue
		}
		return nil, nil, fmt.Errorf("no tool resolver registered for runtime type %q tool %q", runtimeType, binding.Name)
	}
	return tools, toolsets, nil
}

func (r *Registry) RuntimeTypes() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(r.resolvers)+len(r.toolsetResolvers))
	for key := range r.resolvers {
		seen[key] = struct{}{}
		out = append(out, key)
	}
	for key := range r.toolsetResolvers {
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func normalizeRuntimeType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return value
	case "go", "builtin", "function":
		return "internal"
	case "sandbox-tool", "sandbox_tools":
		return "sandbox"
	case "mcp-toolset", "mcp_server":
		return "mcp"
	default:
		return value
	}
}
