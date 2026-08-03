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

package routers

import (
	"net/http"

	"google.golang.org/adk/server/adkrest/controllers"
)

// MCPAPIRouter defines MCP registry/discovery routes for Agent Builder.
type MCPAPIRouter struct {
	controller *controllers.MCPAPIController
}

func NewMCPAPIRouter(controller *controllers.MCPAPIController) *MCPAPIRouter {
	return &MCPAPIRouter{controller: controller}
}

func (r *MCPAPIRouter) Routes() Routes {
	return Routes{
		Route{
			Name:        "ListMCPServers",
			Methods:     []string{http.MethodGet},
			Pattern:     "/mcp/servers",
			HandlerFunc: r.controller.ListServersHandler,
		},
		Route{
			Name:        "TestMCPServer",
			Methods:     []string{http.MethodPost},
			Pattern:     "/mcp/servers/{id}/test",
			HandlerFunc: r.controller.TestServerHandler,
		},
		Route{
			Name:        "DiscoverMCPServerTools",
			Methods:     []string{http.MethodGet, http.MethodPost},
			Pattern:     "/mcp/servers/{id}/discover",
			HandlerFunc: r.controller.DiscoverToolsHandler,
		},
	}
}
