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
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
)

func TestCompileV1UsesOnlyTypedConnector(t *testing.T) {
	binding := runtimeplan.ToolBinding{
		Name:        "workspace.read",
		RuntimeType: "builtin",
		Runtime:     map[string]interface{}{"url": "https://must-not-be-used.example"},
		Execution:   map[string]interface{}{"placement": "runtime"},
	}

	compiled, err := (Compiler{}).CompileV1(binding, ConnectorSpec{
		Kind: ConnectorSandbox,
		Sandbox: &SandboxConnector{
			Capability: "workspace.read",
		},
	})
	if err != nil {
		t.Fatalf("CompileV1() error = %v", err)
	}
	if compiled.Legacy {
		t.Fatal("CompileV1() Legacy = true, want false")
	}
	if compiled.Connector.CanonicalKind() != ConnectorSandbox || compiled.Connector.Sandbox.Capability != "workspace.read" {
		t.Fatalf("compiled connector = %#v", compiled.Connector)
	}
}

func TestCompileV1RejectsIncompleteBuiltin(t *testing.T) {
	_, err := (Compiler{}).CompileV1(runtimeplan.ToolBinding{Name: "load_memory"}, ConnectorSpec{
		Kind:    ConnectorBuiltin,
		Builtin: &BuiltinConnector{BuiltinID: "load_memory"},
	})
	if err == nil {
		t.Fatal("CompileV1() error = nil, want missing implementationVersion error")
	}
}

func TestCompileLegacyBuiltinAllowsMissingImplementationVersion(t *testing.T) {
	compiled, err := (Compiler{}).CompileLegacy(runtimeplan.ToolBinding{
		Name:        "load_memory",
		RuntimeType: "builtin",
	})
	if err != nil {
		t.Fatalf("CompileLegacy() error = %v", err)
	}
	if !compiled.Legacy {
		t.Fatal("CompileLegacy() Legacy = false, want true")
	}
	if compiled.Connector.Builtin == nil || compiled.Connector.Builtin.BuiltinID != "load_memory" {
		t.Fatalf("compiled connector = %#v", compiled.Connector)
	}
	if compiled.Connector.Builtin.ImplementationVersion != "" {
		t.Fatalf("ImplementationVersion = %q, want empty legacy pin", compiled.Connector.Builtin.ImplementationVersion)
	}
}

func TestCompileLegacySandboxUsesExplicitCapability(t *testing.T) {
	compiled, err := (Compiler{}).CompileLegacy(runtimeplan.ToolBinding{
		Name:        "skill.pull",
		RuntimeType: "sandbox",
		Execution: map[string]interface{}{
			"executorCapability": "git.pull",
		},
	})
	if err != nil {
		t.Fatalf("CompileLegacy() error = %v", err)
	}
	if compiled.Connector.Sandbox == nil || compiled.Connector.Sandbox.Capability != "git.pull" {
		t.Fatalf("compiled connector = %#v", compiled.Connector)
	}
}

func TestCompileLegacySandboxPlacementCorrectsStaleBuiltinMarker(t *testing.T) {
	compiled, err := (Compiler{}).CompileLegacy(runtimeplan.ToolBinding{
		Name:        "workspace.read",
		RuntimeType: "builtin",
		Execution: map[string]interface{}{
			"placement": "sandbox",
		},
	})
	if err != nil {
		t.Fatalf("CompileLegacy() error = %v", err)
	}
	if compiled.Connector.CanonicalKind() != ConnectorSandbox {
		t.Fatalf("connector kind = %q, want sandbox", compiled.Connector.CanonicalKind())
	}
	if compiled.Connector.Sandbox == nil || compiled.Connector.Sandbox.Capability != "workspace.read" {
		t.Fatalf("compiled connector = %#v", compiled.Connector)
	}
}

func TestCompileLegacyServiceRefusesInference(t *testing.T) {
	_, err := (Compiler{}).CompileLegacy(runtimeplan.ToolBinding{
		Name:        "skill.get",
		RuntimeType: "service",
		Runtime: map[string]interface{}{
			"url": "https://hub.internal/v1/skills/a",
		},
	})
	if err == nil {
		t.Fatal("CompileLegacy() error = nil, want refusal to infer service contract")
	}
}

func TestCompileLegacyHTTPRefusesRawURL(t *testing.T) {
	_, err := (Compiler{}).CompileLegacy(runtimeplan.ToolBinding{
		Name:        "weather.get",
		RuntimeType: "http",
		Runtime: map[string]interface{}{
			"url":    "https://api.example.com/weather",
			"method": "GET",
		},
	})
	if err == nil {
		t.Fatal("CompileLegacy() error = nil, want raw URL migration error")
	}
}

func TestCompileLegacyMCPPinsServerAndRemoteName(t *testing.T) {
	compiled, err := (Compiler{}).CompileLegacy(runtimeplan.ToolBinding{
		Name:        "github.get_issue",
		RuntimeType: "mcp",
		Runtime: map[string]interface{}{
			"server": "github",
			"name":   "get_issue",
		},
	})
	if err != nil {
		t.Fatalf("CompileLegacy() error = %v", err)
	}
	if compiled.Connector.MCP == nil || compiled.Connector.MCP.ConnectionRef != "github" || compiled.Connector.MCP.RemoteToolName != "get_issue" {
		t.Fatalf("compiled connector = %#v", compiled.Connector)
	}
}
