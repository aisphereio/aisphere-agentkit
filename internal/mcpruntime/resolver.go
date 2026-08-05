// Package mcpruntime turns Hub MCP tool bindings into lazy ADK toolsets.
// Runtime configuration owns endpoint/credential material; Hub owns which
// server and remote tool are authorized for the Agent snapshot.
package mcpruntime

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

func (r Resolver) ResolveToolset(binding runtimeplan.ToolBinding) (tool.Toolset, error) {
	if r.Config == nil {
		return nil, fmt.Errorf("runtime config is required for MCP tool %q", binding.Name)
	}
	server := firstNonEmpty(
		stringFromMap(binding.Runtime, "server"),
		stringFromMap(binding.Execution, "server"),
	)
	if server == "" {
		return nil, fmt.Errorf("MCP tool %q has no server id", binding.Name)
	}
	if _, ok := r.Config.MCPServer(server); !ok {
		return nil, fmt.Errorf("MCP server %q is not registered in runtime config", server)
	}
	args := map[string]any{"server": server}
	if transport := firstNonEmpty(stringFromMap(binding.Runtime, "transport"), stringFromMap(binding.Execution, "transport")); transport != "" {
		args["transport"] = transport
	}
	filter := stringSliceFromMap(binding.Runtime, "toolFilter")
	if len(filter) == 0 {
		filter = stringSliceFromMap(binding.Runtime, "tool_filter")
	}
	if len(filter) == 0 {
		filter = stringSliceFromMap(binding.Execution, "toolFilter")
	}
	// A Hub Tool is one selected remote MCP function. Restrict the toolset to
	// that function when the catalog did not provide an explicit filter.
	if len(filter) == 0 {
		if remoteName := firstNonEmpty(binding.RuntimeName, stringFromMap(binding.Runtime, "name"), stringFromMap(binding.Execution, "name")); remoteName != "" {
			filter = []string{remoteName}
		} else {
			filter = []string{binding.Name}
		}
	}
	args["tool_filter"] = filter
	return configurable.NewMCPToolset(runtimeconfig.WithConfig(context.Background(), r.Config), args)
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil || values[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(values[key]))
}

func stringSliceFromMap(values map[string]interface{}, key string) []string {
	if values == nil {
		return nil
	}
	switch raw := values[key].(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if value := strings.TrimSpace(fmt.Sprint(item)); value != "" && value != "<nil>" {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
