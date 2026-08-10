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

import (
	"fmt"
	"strings"
)

// ConnectorSpec is the typed execution contract produced by ToolCompiler.
// Exactly one connector payload must be present and it must match Kind.
//
// Tool semantics (skill.publish, workspace.read, knowledge.query, ...),
// discovery source (builtin manifest, MCP discovery, OpenAPI import, ...), and
// execution connector are intentionally independent concepts. Only this type
// selects the Runtime execution adapter.
type ConnectorSpec struct {
	Kind    string
	Builtin *BuiltinConnector
	Service *ServiceConnector
	Sandbox *SandboxConnector
	MCP     *MCPConnector
	HTTP    *HTTPConnector
}

// BuiltinConnector identifies code compiled into the trusted Runtime binary.
type BuiltinConnector struct {
	BuiltinID             string
	ImplementationVersion string
	DescriptorDigest      string
}

// ServiceConnector identifies a trusted first-party AISphere service
// operation. Service is a logical service id, never an arbitrary URL.
type ServiceConnector struct {
	Service         string
	Operation       string
	ContractVersion string
	TargetResolver  string
}

// SandboxConnector identifies a low-level executor capability. The capability
// is deliberately independent from the model-facing Tool name.
type SandboxConnector struct {
	Capability           string
	RequiredCapabilities []string
	PackageRef           string
}

// MCPConnector pins one discovered remote MCP tool to an immutable ToolVersion.
type MCPConnector struct {
	ConnectionRef          string
	RemoteToolName         string
	ProtocolVersion        string
	DiscoveredSchemaDigest string
}

// HTTPConnector invokes an external HTTP connection managed by the platform.
// Endpoint credentials and base URL belong to ConnectionRef; ToolVersion only
// carries the stable method/path/mapping contract.
type HTTPConnector struct {
	ConnectionRef   string
	Method          string
	PathTemplate    string
	RequestMapping  map[string]any
	ResponseMapping map[string]any
}

// Validate rejects ambiguous or under-specified connectors. This is the
// fail-closed boundary ToolCompiler uses before an adapter may be selected.
func (c ConnectorSpec) Validate() error {
	kind := normalizeConnectorKind(c.Kind)
	if kind == "" {
		return fmt.Errorf("connector kind is required")
	}
	if c.payloadCount() != 1 {
		return fmt.Errorf("connector %q must contain exactly one connector payload", kind)
	}

	switch kind {
	case ConnectorBuiltin:
		if c.Builtin == nil {
			return kindMismatch(kind)
		}
		if strings.TrimSpace(c.Builtin.BuiltinID) == "" {
			return fmt.Errorf("builtin connector builtinId is required")
		}
		if strings.TrimSpace(c.Builtin.ImplementationVersion) == "" {
			return fmt.Errorf("builtin connector implementationVersion is required")
		}
	case ConnectorService:
		if c.Service == nil {
			return kindMismatch(kind)
		}
		if strings.TrimSpace(c.Service.Service) == "" {
			return fmt.Errorf("service connector service is required")
		}
		if strings.TrimSpace(c.Service.Operation) == "" {
			return fmt.Errorf("service connector operation is required")
		}
	case ConnectorSandbox:
		if c.Sandbox == nil {
			return kindMismatch(kind)
		}
		if strings.TrimSpace(c.Sandbox.Capability) == "" {
			return fmt.Errorf("sandbox connector capability is required")
		}
	case ConnectorMCP:
		if c.MCP == nil {
			return kindMismatch(kind)
		}
		if strings.TrimSpace(c.MCP.ConnectionRef) == "" {
			return fmt.Errorf("mcp connector connectionRef is required")
		}
		if strings.TrimSpace(c.MCP.RemoteToolName) == "" {
			return fmt.Errorf("mcp connector remoteToolName is required")
		}
	case ConnectorHTTP:
		if c.HTTP == nil {
			return kindMismatch(kind)
		}
		if strings.TrimSpace(c.HTTP.ConnectionRef) == "" {
			return fmt.Errorf("http connector connectionRef is required")
		}
		if strings.TrimSpace(c.HTTP.Method) == "" {
			return fmt.Errorf("http connector method is required")
		}
		if strings.TrimSpace(c.HTTP.PathTemplate) == "" {
			return fmt.Errorf("http connector pathTemplate is required")
		}
	default:
		return fmt.Errorf("unsupported connector kind %q", kind)
	}
	return nil
}

func (c ConnectorSpec) CanonicalKind() string {
	return normalizeConnectorKind(c.Kind)
}

func (c ConnectorSpec) payloadCount() int {
	count := 0
	if c.Builtin != nil {
		count++
	}
	if c.Service != nil {
		count++
	}
	if c.Sandbox != nil {
		count++
	}
	if c.MCP != nil {
		count++
	}
	if c.HTTP != nil {
		count++
	}
	return count
}

func kindMismatch(kind string) error {
	return fmt.Errorf("connector kind %q does not match connector payload", kind)
}
