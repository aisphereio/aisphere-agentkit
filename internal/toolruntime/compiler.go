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

	"google.golang.org/adk/internal/runtimeplan"
)

// CompiledToolBinding is the only Tool shape execution adapters should consume.
// Binding still carries model/policy fields during the RuntimePlan migration;
// Connector is already typed and must be used for adapter routing.
type CompiledToolBinding struct {
	Binding   runtimeplan.ToolBinding
	Connector ConnectorSpec
	Legacy    bool
}

// Compiler converts immutable Tool bindings into a typed execution connector.
// New V1 snapshots call CompileV1. CompileLegacy exists only while old Hub
// runtime/execution map snapshots are still readable.
type Compiler struct{}

// CompileV1 validates an already typed connector. It never reads runtime.type,
// execution.runner/placement or other compatibility maps to choose an adapter.
func (Compiler) CompileV1(binding runtimeplan.ToolBinding, connector ConnectorSpec) (CompiledToolBinding, error) {
	if strings.TrimSpace(binding.Name) == "" {
		return CompiledToolBinding{}, fmt.Errorf("tool name is required")
	}
	if err := connector.Validate(); err != nil {
		return CompiledToolBinding{}, fmt.Errorf("compile tool %q connector: %w", binding.Name, err)
	}
	connector.Kind = connector.CanonicalKind()
	return CompiledToolBinding{Binding: binding, Connector: connector}, nil
}

// CompileLegacy is the single compatibility boundary for pre-V1 Hub snapshots.
// It intentionally supports only mappings whose trust boundary is unambiguous.
// Unsupported legacy combinations fail closed instead of manufacturing a V1
// connection or changing execution placement.
func (Compiler) CompileLegacy(binding runtimeplan.ToolBinding) (CompiledToolBinding, error) {
	if strings.TrimSpace(binding.Name) == "" {
		return CompiledToolBinding{}, fmt.Errorf("tool name is required")
	}

	kind := connectorKindForBinding(binding)
	connector, err := legacyConnector(binding, kind)
	if err != nil {
		return CompiledToolBinding{}, fmt.Errorf("compile legacy tool %q: %w", binding.Name, err)
	}
	return CompiledToolBinding{Binding: binding, Connector: connector, Legacy: true}, nil
}

func legacyConnector(binding runtimeplan.ToolBinding, kind string) (ConnectorSpec, error) {
	switch kind {
	case ConnectorBuiltin:
		id := firstLegacyString(binding.RuntimeName, stringMapValue(binding.Runtime, "name"), binding.Name)
		if id == "" {
			return ConnectorSpec{}, fmt.Errorf("builtin id is required")
		}
		return ConnectorSpec{
			Kind: ConnectorBuiltin,
			Builtin: &BuiltinConnector{
				BuiltinID: id,
				ImplementationVersion: firstLegacyString(
					stringMapValue(binding.Runtime, "implementationVersion"),
					stringMapValue(binding.Runtime, "implementation_version"),
					stringMapValue(binding.Metadata, "implementationVersion"),
					stringMapValue(binding.Metadata, "implementation_version"),
				),
				DescriptorDigest: firstLegacyString(
					stringMapValue(binding.Runtime, "descriptorDigest"),
					stringMapValue(binding.Runtime, "descriptor_digest"),
					stringMapValue(binding.Metadata, "descriptorDigest"),
					stringMapValue(binding.Metadata, "descriptor_digest"),
				),
			},
		}, nil

	case ConnectorSandbox:
		capability := firstLegacyString(
			stringMapValue(binding.Execution, "executorCapability"),
			stringMapValue(binding.Execution, "executor_capability"),
			stringMapValue(binding.Runtime, "executorCapability"),
			stringMapValue(binding.Runtime, "executor_capability"),
			stringMapValue(binding.Runtime, "capability"),
			binding.Name, // compatibility only; V1 requires an explicit capability.
		)
		if capability == "" {
			return ConnectorSpec{}, fmt.Errorf("sandbox capability is required")
		}
		return ConnectorSpec{
			Kind: ConnectorSandbox,
			Sandbox: &SandboxConnector{
				Capability:           capability,
				RequiredCapabilities: append([]string(nil), binding.Capabilities...),
				PackageRef: firstLegacyString(
					stringMapValue(binding.Execution, "packageRef"),
					stringMapValue(binding.Execution, "package_ref"),
				),
			},
		}, nil

	case ConnectorService:
		service := firstLegacyString(
			stringMapValue(binding.Runtime, "service"),
			stringMapValue(binding.Runtime, "serviceId"),
			stringMapValue(binding.Runtime, "service_id"),
		)
		operation := firstLegacyString(
			stringMapValue(binding.Runtime, "operation"),
			stringMapValue(binding.Runtime, "operationId"),
			stringMapValue(binding.Runtime, "operation_id"),
		)
		if service == "" || operation == "" {
			return ConnectorSpec{}, fmt.Errorf("legacy service connector requires explicit service and operation; refusing to infer from URL or tool name")
		}
		return ConnectorSpec{
			Kind: ConnectorService,
			Service: &ServiceConnector{
				Service:         service,
				Operation:       operation,
				ContractVersion: firstLegacyString(stringMapValue(binding.Runtime, "contractVersion"), stringMapValue(binding.Runtime, "contract_version")),
				TargetResolver:  firstLegacyString(stringMapValue(binding.Runtime, "targetResolver"), stringMapValue(binding.Runtime, "target_resolver")),
			},
		}, nil

	case ConnectorMCP:
		connectionRef := firstLegacyString(
			stringMapValue(binding.Runtime, "connectionRef"),
			stringMapValue(binding.Runtime, "connection_ref"),
			stringMapValue(binding.Runtime, "server"), // pre-V1 logical MCP server name.
		)
		remoteName := firstLegacyString(
			stringMapValue(binding.Runtime, "remoteToolName"),
			stringMapValue(binding.Runtime, "remote_tool_name"),
			binding.RuntimeName,
			stringMapValue(binding.Runtime, "name"),
			binding.Name,
		)
		if connectionRef == "" || remoteName == "" {
			return ConnectorSpec{}, fmt.Errorf("legacy mcp connector requires server/connectionRef and remote tool name")
		}
		return ConnectorSpec{
			Kind: ConnectorMCP,
			MCP: &MCPConnector{
				ConnectionRef:          connectionRef,
				RemoteToolName:         remoteName,
				ProtocolVersion:        firstLegacyString(stringMapValue(binding.Runtime, "protocolVersion"), stringMapValue(binding.Runtime, "protocol_version")),
				DiscoveredSchemaDigest: firstLegacyString(stringMapValue(binding.Runtime, "discoveredSchemaDigest"), stringMapValue(binding.Runtime, "discovered_schema_digest")),
			},
		}, nil

	case ConnectorHTTP:
		connectionRef := firstLegacyString(
			stringMapValue(binding.Runtime, "connectionRef"),
			stringMapValue(binding.Runtime, "connection_ref"),
		)
		method := firstLegacyString(stringMapValue(binding.Runtime, "method"))
		pathTemplate := firstLegacyString(
			stringMapValue(binding.Runtime, "pathTemplate"),
			stringMapValue(binding.Runtime, "path_template"),
		)
		if connectionRef == "" || method == "" || pathTemplate == "" {
			return ConnectorSpec{}, fmt.Errorf("legacy http connector requires explicit connectionRef, method and pathTemplate; raw url/openapi definitions must be migrated in Hub")
		}
		return ConnectorSpec{
			Kind: ConnectorHTTP,
			HTTP: &HTTPConnector{
				ConnectionRef:   connectionRef,
				Method:          strings.ToUpper(method),
				PathTemplate:    pathTemplate,
				RequestMapping:  mapMapValue(binding.Runtime, "requestMapping", "request_mapping"),
				ResponseMapping: mapMapValue(binding.Runtime, "responseMapping", "response_mapping"),
			},
		}, nil
	default:
		return ConnectorSpec{}, fmt.Errorf("unsupported connector kind %q", kind)
	}
}

func stringMapValue(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func mapMapValue(values map[string]interface{}, keys ...string) map[string]any {
	for _, key := range keys {
		if values == nil {
			continue
		}
		raw, ok := values[key]
		if !ok || raw == nil {
			continue
		}
		if typed, ok := raw.(map[string]any); ok {
			out := make(map[string]any, len(typed))
			for k, v := range typed {
				out[k] = v
			}
			return out
		}
	}
	return nil
}

func firstLegacyString(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
