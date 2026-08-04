package mcpruntime

import (
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimeplan"
)

func TestResolverBuildsLazyToolsetFromRegisteredServer(t *testing.T) {
	cfg := &runtimeconfig.Config{MCP: runtimeconfig.MCPConfig{Servers: map[string]runtimeconfig.MCPServerConfig{
		"novel_assets": {Endpoint: "http://127.0.0.1:8090/mcp", Transport: "streamable_http"},
	}}}
	set, err := (Resolver{Config: cfg}).ResolveToolset(runtimeplan.ToolBinding{
		Name: "list_split_books", RuntimeType: "mcp",
		Runtime: map[string]interface{}{"server": "novel_assets", "name": "list_split_books"},
	})
	if err != nil {
		t.Fatalf("ResolveToolset() error = %v", err)
	}
	if set == nil {
		t.Fatal("ResolveToolset() returned nil toolset")
	}
}

func TestResolverFailsClosedForUnregisteredServer(t *testing.T) {
	_, err := (Resolver{Config: &runtimeconfig.Config{}}).ResolveToolset(runtimeplan.ToolBinding{
		Name: "list_split_books", RuntimeType: "mcp",
		Runtime: map[string]interface{}{"server": "missing"},
	})
	if err == nil {
		t.Fatal("ResolveToolset() error = nil, want missing server error")
	}
}
