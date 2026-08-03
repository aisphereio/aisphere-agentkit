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

package controllers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/version"
)

// MCPAPIController exposes configured MCP servers and live tool discovery to
// the Agent Builder UI. It never returns configured header values.
type MCPAPIController struct {
	cfg *runtimeconfig.Config
}

func NewMCPAPIController(cfg *runtimeconfig.Config) *MCPAPIController {
	return &MCPAPIController{cfg: cfg}
}

func (c *MCPAPIController) config() *runtimeconfig.Config {
	if c != nil && c.cfg != nil {
		return c.cfg
	}
	return runtimeconfig.FromContext(nil)
}

// ListServersHandler handles GET /mcp/servers.
func (c *MCPAPIController) ListServersHandler(rw http.ResponseWriter, req *http.Request) {
	cfg := c.config()
	EncodeJSONResponse(map[string]any{
		"servers": cfg.FrontendMCPServers(),
	}, http.StatusOK, rw)
}

// TestServerHandler handles POST /mcp/servers/{id}/test.
func (c *MCPAPIController) TestServerHandler(rw http.ResponseWriter, req *http.Request) {
	id := mux.Vars(req)["id"]
	server, ok := c.config().MCPServer(id)
	if !ok {
		http.Error(rw, "mcp server not found", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeoutForMCPServer(server))
	defer cancel()

	session, err := connectMCPServer(ctx, id, server)
	if err != nil {
		EncodeJSONResponse(map[string]any{"ok": false, "error": err.Error()}, http.StatusBadGateway, rw)
		return
	}
	defer session.Close()
	if err := session.Ping(ctx, &mcp.PingParams{}); err != nil {
		EncodeJSONResponse(map[string]any{"ok": false, "error": err.Error()}, http.StatusBadGateway, rw)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true, "server_id": id}, http.StatusOK, rw)
}

// DiscoverToolsHandler handles POST/GET /mcp/servers/{id}/discover.
func (c *MCPAPIController) DiscoverToolsHandler(rw http.ResponseWriter, req *http.Request) {
	id := mux.Vars(req)["id"]
	server, ok := c.config().MCPServer(id)
	if !ok {
		http.Error(rw, "mcp server not found", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeoutForMCPServer(server))
	defer cancel()

	session, err := connectMCPServer(ctx, id, server)
	if err != nil {
		EncodeJSONResponse(map[string]any{"ok": false, "error": err.Error()}, http.StatusBadGateway, rw)
		return
	}
	defer session.Close()

	tools, err := listAllMCPTools(ctx, session)
	if err != nil {
		EncodeJSONResponse(map[string]any{"ok": false, "error": err.Error()}, http.StatusBadGateway, rw)
		return
	}
	EncodeJSONResponse(map[string]any{
		"ok":        true,
		"server_id": id,
		"tools":     tools,
	}, http.StatusOK, rw)
}

type frontendMCPTool struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	InputSchema  any    `json:"inputSchema,omitempty"`
	OutputSchema any    `json:"outputSchema,omitempty"`
}

func listAllMCPTools(ctx context.Context, session *mcp.ClientSession) ([]frontendMCPTool, error) {
	out := []frontendMCPTool{}
	cursor := ""
	for {
		resp, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		for _, t := range resp.Tools {
			out = append(out, frontendMCPTool{
				Name:         t.Name,
				Description:  t.Description,
				InputSchema:  t.InputSchema,
				OutputSchema: t.OutputSchema,
			})
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return out, nil
}

func connectMCPServer(ctx context.Context, id string, server runtimeconfig.MCPServerConfig) (*mcp.ClientSession, error) {
	transportName := server.Transport
	if transportName == "" {
		transportName = "streamable_http"
	}
	if transportName != "streamable_http" && transportName != "http" {
		return nil, fmt.Errorf("only streamable_http MCP discovery is supported for configured server %q, got %q", id, transportName)
	}
	endpoint := os.ExpandEnv(server.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("mcp server %q endpoint is empty", id)
	}
	client := http.DefaultClient
	if len(server.Headers) > 0 {
		client = &http.Client{Transport: &mcpHeaderTransport{headers: server.Headers}}
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "adk-builder-mcp-discovery", Version: version.Version}, nil)
	return mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client}, nil)
}

type mcpHeaderTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *mcpHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	for k, v := range t.headers {
		clone.Header.Set(k, os.ExpandEnv(v))
	}
	return base.RoundTrip(clone)
}

func timeoutForMCPServer(server runtimeconfig.MCPServerConfig) time.Duration {
	seconds := server.TimeoutSeconds
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}
