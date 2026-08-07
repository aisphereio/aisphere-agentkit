// Package toolruntime maps Hub tool bindings to executable ADK tool adapters.
// It is the single seam where sandbox tools, MCP tools, and Runtime-owned
// builtin tools become visible to the ADK-Go agent.
package toolruntime

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/tool"
)

const (
	ConnectorBuiltin = "builtin"
	ConnectorSandbox = "sandbox"
	ConnectorMCP     = "mcp"
	ConnectorHTTP    = "http"
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

func (r *Registry) Register(connectorKind string, resolver Resolver) error {
	connectorKind = normalizeConnectorKind(connectorKind)
	if connectorKind == "" {
		return fmt.Errorf("connector kind is required")
	}
	if resolver == nil {
		return fmt.Errorf("resolver is required for connector kind %q", connectorKind)
	}
	if r.resolvers == nil {
		r.resolvers = map[string]Resolver{}
	}
	r.resolvers[connectorKind] = resolver
	return nil
}

func (r *Registry) RegisterToolset(connectorKind string, resolver ToolsetResolver) error {
	connectorKind = normalizeConnectorKind(connectorKind)
	if connectorKind == "" {
		return fmt.Errorf("connector kind is required")
	}
	if resolver == nil {
		return fmt.Errorf("toolset resolver is required for connector kind %q", connectorKind)
	}
	if r.toolsetResolvers == nil {
		r.toolsetResolvers = map[string]ToolsetResolver{}
	}
	r.toolsetResolvers[connectorKind] = resolver
	return nil
}

func (r *Registry) Resolve(plan *runtimeplan.RuntimePlan) ([]tool.Tool, error) {
	tools, _, err := r.ResolveAll(plan)
	return tools, err
}

// ResolveAll resolves only the Tool bindings explicitly selected in the
// RuntimePlan. Registry contents are an implementation capability superset and
// are never implicitly exposed to an Agent.
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
		connectorKind := normalizeConnectorKind(binding.RuntimeType)
		if connectorKind == "" {
			// Transitional compatibility with pre-V1 Hub snapshots. New V1
			// contracts must always carry a typed connector kind.
			connectorKind = ConnectorBuiltin
		}
		if resolver := r.resolvers[connectorKind]; resolver != nil {
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
		if resolver := r.toolsetResolvers[connectorKind]; resolver != nil {
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
		return nil, nil, fmt.Errorf("no tool resolver registered for connector kind %q tool %q", connectorKind, binding.Name)
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

func normalizeConnectorKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return value
	case "internal", "go", "function", ConnectorBuiltin:
		return ConnectorBuiltin
	case "sandbox-tool", "sandbox_tools", ConnectorSandbox:
		return ConnectorSandbox
	case "mcp-toolset", "mcp_server", ConnectorMCP:
		return ConnectorMCP
	default:
		return value
	}
}
