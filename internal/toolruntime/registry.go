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

// Package toolruntime maps Hub tool bindings to executable ADK tool adapters.
// It is the single seam where Runtime builtins, AISphere service operations,
// sandbox capabilities, MCP tools, and HTTP tools become visible to ADK-Go.
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
	ConnectorService = "service"
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
		connectorKind := connectorKindForBinding(binding)
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

// connectorKindForBinding contains the only compatibility correction for the
// legacy map-based Hub Tool contract. Historical Hub builtin seeds tagged
// workspace/browser tools as runtime.type=builtin while execution.placement was
// already sandbox. The trusted Runtime must never execute those tools locally,
// so sandbox placement wins over the stale builtin marker.
//
// Typed Tool V1 ExecutionSpec removes this compatibility rule: connector.kind
// will be authoritative and this function can collapse to normalizeConnectorKind.
func connectorKindForBinding(binding runtimeplan.ToolBinding) string {
	connectorKind := normalizeConnectorKind(binding.RuntimeType)
	placement, _ := binding.Execution["placement"].(string)
	if strings.EqualFold(strings.TrimSpace(placement), ConnectorSandbox) &&
		(connectorKind == "" || connectorKind == ConnectorBuiltin) {
		return ConnectorSandbox
	}
	if connectorKind == "" {
		// Transitional compatibility with pre-V1 Hub snapshots. New V1 contracts
		// must always carry a typed connector kind.
		return ConnectorBuiltin
	}
	return connectorKind
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
		// Legacy `internal` meant an in-process Go/function implementation. Do
		// not reinterpret it as ConnectorService during migration.
		return ConnectorBuiltin
	case "internal-service", "internal_service", "platform-service", "platform_service", ConnectorService:
		return ConnectorService
	case "sandbox-tool", "sandbox_tools", ConnectorSandbox:
		return ConnectorSandbox
	case "mcp-toolset", "mcp_server", ConnectorMCP:
		return ConnectorMCP
	default:
		return value
	}
}
