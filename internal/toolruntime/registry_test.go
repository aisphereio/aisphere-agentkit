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

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/tool"
)

type fakeTool struct{ name string }

func (f fakeTool) Name() string      { return f.name }
func (fakeTool) Description() string { return "fake" }
func (fakeTool) IsLongRunning() bool { return false }

func TestRegistryResolvesPlanToolsByRuntimeType(t *testing.T) {
	registry := New()
	if err := registry.Register("sandbox", ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	tools, err := registry.Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox"}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "workspace.read" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestLegacySandboxPlacementOverridesStaleBuiltinType(t *testing.T) {
	registry := New()
	if err := registry.Register(ConnectorBuiltin, ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: "builtin:" + binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register(builtin) error = %v", err)
	}
	if err := registry.Register(ConnectorSandbox, ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: "sandbox:" + binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register(sandbox) error = %v", err)
	}

	tools, err := registry.Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{
		Name:        "workspace.read",
		RuntimeType: "builtin",
		Execution:   map[string]interface{}{"placement": "sandbox"},
	}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "sandbox:workspace.read" {
		t.Fatalf("legacy sandbox placement resolved through wrong adapter: %+v", tools)
	}
}

func TestRegistryFailsClosedForUnknownRuntimeType(t *testing.T) {
	_, err := New().Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox"}}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
}

func TestRegistryDoesNotExposeUnselectedBuiltins(t *testing.T) {
	registry := New()
	if err := registry.Register("builtin", ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	tools, err := registry.Resolve(&runtimeplan.RuntimePlan{})
	if err != nil {
		t.Fatalf("Resolve(empty plan) error = %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("Resolve(empty plan) tools = %d, want 0", len(tools))
	}

	tools, err = registry.Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{
		{Name: "memory.search", RuntimeType: "builtin"},
	}})
	if err != nil {
		t.Fatalf("Resolve(selected builtin) error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "memory.search" {
		t.Fatalf("unexpected selected tools: %+v", tools)
	}
}

func TestLegacyInternalAliasNormalizesToBuiltin(t *testing.T) {
	registry := New()
	if err := registry.Register("internal", ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.RuntimeTypes(); len(got) != 1 || got[0] != ConnectorBuiltin {
		t.Fatalf("RuntimeTypes() = %#v, want [%q]", got, ConnectorBuiltin)
	}
}

func TestServiceConnectorHasDedicatedCanonicalKind(t *testing.T) {
	registry := New()
	if err := registry.Register("internal_service", ResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Tool, error) {
		return fakeTool{name: "service:" + binding.Name}, nil
	})); err != nil {
		t.Fatalf("Register(service) error = %v", err)
	}

	if got := registry.RuntimeTypes(); len(got) != 1 || got[0] != ConnectorService {
		t.Fatalf("RuntimeTypes() = %#v, want [%q]", got, ConnectorService)
	}

	tools, err := registry.Resolve(&runtimeplan.RuntimePlan{Tools: []runtimeplan.ToolBinding{{
		Name:        "skill.publish",
		RuntimeType: "platform-service",
	}}})
	if err != nil {
		t.Fatalf("Resolve(service) error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "service:skill.publish" {
		t.Fatalf("unexpected service tools: %+v", tools)
	}
}

func TestLegacyInternalDoesNotAliasToService(t *testing.T) {
	if got := normalizeConnectorKind("internal"); got != ConnectorBuiltin {
		t.Fatalf("normalizeConnectorKind(internal) = %q, want %q", got, ConnectorBuiltin)
	}
	if got := normalizeConnectorKind("internal-service"); got != ConnectorService {
		t.Fatalf("normalizeConnectorKind(internal-service) = %q, want %q", got, ConnectorService)
	}
}

type fakeToolset struct{ name string }

func (f fakeToolset) Name() string { return f.name }
func (f fakeToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return []tool.Tool{fakeTool{name: f.name + ".tool"}}, nil
}

func TestRegistryResolvesToolsetsByRuntimeType(t *testing.T) {
	registry := New()
	if err := registry.RegisterToolset("mcp", ToolsetResolverFunc(func(binding runtimeplan.ToolBinding) (tool.Toolset, error) {
		return fakeToolset{name: binding.Name}, nil
	})); err != nil {
		t.Fatalf("RegisterToolset() error = %v", err)
	}
	tools, toolsets, err := registry.ResolveAll(&runtimeplan.RuntimePlan{
		Tools: []runtimeplan.ToolBinding{{Name: "novel_assets", RuntimeType: "mcp"}},
	})
	if err != nil {
		t.Fatalf("ResolveAll() error = %v", err)
	}
	if len(tools) != 0 || len(toolsets) != 1 || toolsets[0].Name() != "novel_assets" {
		t.Fatalf("unexpected resolved tools/toolsets: %d/%d", len(tools), len(toolsets))
	}
}
