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

package toolruntime

import "testing"

func TestConnectorSpecValidatesAllCanonicalKinds(t *testing.T) {
	tests := []struct {
		name string
		spec ConnectorSpec
	}{
		{name: "builtin", spec: ConnectorSpec{Kind: ConnectorBuiltin, Builtin: &BuiltinConnector{BuiltinID: "load_memory", ImplementationVersion: "1"}}},
		{name: "service", spec: ConnectorSpec{Kind: ConnectorService, Service: &ServiceConnector{Service: "hub", Operation: "skill.publish", ContractVersion: "v1"}}},
		{name: "sandbox", spec: ConnectorSpec{Kind: ConnectorSandbox, Sandbox: &SandboxConnector{Capability: "workspace.read"}}},
		{name: "mcp", spec: ConnectorSpec{Kind: ConnectorMCP, MCP: &MCPConnector{ConnectionRef: "mcp://github", RemoteToolName: "get_issue", ProtocolVersion: "2025-11-25"}}},
		{name: "http", spec: ConnectorSpec{Kind: ConnectorHTTP, HTTP: &HTTPConnector{ConnectionRef: "connection://weather", Method: "GET", PathTemplate: "/forecast"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.spec.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestConnectorSpecFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		spec ConnectorSpec
	}{
		{name: "unknown kind", spec: ConnectorSpec{Kind: "python", Sandbox: &SandboxConnector{Capability: "python.exec"}}},
		{name: "ambiguous payload", spec: ConnectorSpec{Kind: ConnectorSandbox, Sandbox: &SandboxConnector{Capability: "workspace.read"}, HTTP: &HTTPConnector{ConnectionRef: "c", Method: "GET", PathTemplate: "/"}}},
		{name: "sandbox missing capability", spec: ConnectorSpec{Kind: ConnectorSandbox, Sandbox: &SandboxConnector{}}},
		{name: "service arbitrary without operation", spec: ConnectorSpec{Kind: ConnectorService, Service: &ServiceConnector{Service: "hub"}}},
		{name: "mcp missing connection", spec: ConnectorSpec{Kind: ConnectorMCP, MCP: &MCPConnector{RemoteToolName: "search"}}},
		{name: "http missing connection", spec: ConnectorSpec{Kind: ConnectorHTTP, HTTP: &HTTPConnector{Method: "GET", PathTemplate: "/"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.spec.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want fail-closed error")
			}
		})
	}
}

func TestConnectorKindAliasesDoNotChangeTrustBoundary(t *testing.T) {
	tests := map[string]string{
		"internal":         ConnectorBuiltin,
		"function":         ConnectorBuiltin,
		"internal-service": ConnectorService,
		"platform_service": ConnectorService,
		"sandbox-tool":     ConnectorSandbox,
		"mcp_server":       ConnectorMCP,
	}
	for in, want := range tests {
		if got := normalizeConnectorKind(in); got != want {
			t.Fatalf("normalizeConnectorKind(%q) = %q, want %q", in, got, want)
		}
	}
}
