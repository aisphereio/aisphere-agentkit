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

// configutils.go provides utility functions for working with configurable agents.
package configurable

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"
	"google.golang.org/adk/tool/bookpreprocessortool"
	"google.golang.org/adk/tool/bookskillruntool"
	"google.golang.org/adk/tool/deleteartifactstool"
	"google.golang.org/adk/tool/envmanagertool"
	"google.golang.org/adk/tool/exampletool"
	"google.golang.org/adk/tool/exitlooptool"
	"google.golang.org/adk/tool/filesretrievaltool"
	"google.golang.org/adk/tool/geminitool"
	"google.golang.org/adk/tool/getuserchoicetool"
	"google.golang.org/adk/tool/listartifactstool"
	"google.golang.org/adk/tool/loadartifactstool"
	"google.golang.org/adk/tool/loadmemorytool"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/adk/tool/novelstoretool"
	"google.golang.org/adk/tool/planruntool"
	"google.golang.org/adk/tool/preloadmemorytool"
	"google.golang.org/adk/tool/projectartifacttool"
	"google.golang.org/adk/tool/requestuserformtool"
	"google.golang.org/adk/tool/saveartifactstool"
	"google.golang.org/adk/tool/sessionworkspacetool"
	"google.golang.org/adk/tool/skillauthoringtool"
	"google.golang.org/adk/tool/uploadstool"
)

type AgentFactory func(ctx context.Context, configBytes []byte, configPath string) (agent.Agent, error)

type ToolFactory func(ctx context.Context, args map[string]any) (tool.Tool, error)

type ToolsetFactory func(ctx context.Context, args map[string]any) (tool.Toolset, error)

var (
	registryMu       sync.RWMutex
	registry         = make(map[string]AgentFactory)
	agentRegistry    = make(map[string]agent.Agent)
	toolRegistry     = make(map[string]any)
	callbackRegistry = make(map[string]any)
)

func init() {
	if err := Register("LlmAgent", newLLMAgent); err != nil {
		panic(err)
	}
	if err := Register("LoopAgent", newLoopAgent); err != nil {
		panic(err)
	}
	if err := Register("ParallelAgent", newParallelAgent); err != nil {
		panic(err)
	}
	if err := Register("SequentialAgent", newSequentialAgent); err != nil {
		panic(err)
	}
	err := RegisterToolFactory("exit_loop", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return exitlooptool.New()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("save_artifact", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return saveartifactstool.New()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("load_artifacts", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return loadartifactstool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("list_artifacts", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return listartifactstool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("delete_artifact", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return deleteartifactstool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("files_retrieval", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return filesretrievaltool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("FilesRetrieval", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return filesretrievaltool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("BookPreprocessorToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return bookpreprocessortool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("BookSkillRunToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return bookskillruntool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("PlanRunToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return planruntool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("ProjectArtifactToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return projectartifacttool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("UploadToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return uploadstool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("NovelStoreToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return novelstoretool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("SkillAuthoringToolset", func(_ context.Context, _ map[string]any) (tool.Toolset, error) {
		return skillauthoringtool.NewToolset()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("SessionWorkspaceToolset", func(_ context.Context, args map[string]any) (tool.Toolset, error) {
		return sessionworkspacetool.NewToolset(sessionworkspacetool.ConfigFromMap(args))
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("load_memory", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return loadmemorytool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("preload_memory", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return preloadmemorytool.New(), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("get_user_choice", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return getuserchoicetool.New()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("request_user_form", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return requestuserformtool.New()
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("EnvToolset", func(ctx context.Context, args map[string]any) (tool.Toolset, error) {
		cfg := envmanagertool.Config{}
		if args != nil {
			b, _ := json.Marshal(args)
			if err := json.Unmarshal(b, &cfg); err != nil {
				return nil, fmt.Errorf("decode EnvToolset args: %w", err)
			}
		}
		if cfg.ConfigPath != "" && !filepath.IsAbs(cfg.ConfigPath) {
			if parentPath, ok := ctx.Value(parentPathKey).(string); ok && parentPath != "" {
				cfg.ConfigPath = filepath.Join(filepath.Dir(parentPath), cfg.ConfigPath)
			}
		}
		return envmanagertool.NewToolset(cfg)
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("google_search", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return geminitool.GoogleSearch{}, nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("url_context", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return geminitool.New("url_context", "url context", &genai.Tool{URLContext: &genai.URLContext{}}), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("google_maps_grounding", func(_ context.Context, _ map[string]any) (tool.Tool, error) {
		return geminitool.New("google_maps_grounding", "google maps grounding", &genai.Tool{GoogleMaps: &genai.GoogleMaps{}}), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("AgentTool", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		if args == nil {
			return nil, fmt.Errorf("args is nil")
		}
		a, ok := args["agent"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("agent not found in args")
		}
		parentPath, ok := ctx.Value(parentPathKey).(string)
		if !ok {
			return nil, fmt.Errorf("parentPath not found in context")
		}

		configPath := stringArg(a, "config_path")
		if configPath == "" {
			return nil, fmt.Errorf("config_path not found in AgentTool args.agent")
		}
		ag, err := ResolveAgentReference(ctx, parentPath, configPath)
		if err != nil {
			return nil, err
		}

		cfg := &agenttool.Config{
			SkipSummarization: boolArg(a, "skip_summarization"),
			ContextMode:       firstNonEmptyString(stringArg(a, "context_mode"), stringArg(args, "context_mode")),
			SessionKey:        firstNonEmptyString(stringArg(a, "session_key"), stringArg(args, "session_key")),
			ParallelSafe:      boolArg(a, "parallel") || boolArg(a, "parallel_safe") || boolArg(args, "parallel") || boolArg(args, "parallel_safe"),
			MaxOutputChars:    intArg(a, "max_output_chars"),
		}
		if cfg.MaxOutputChars <= 0 {
			cfg.MaxOutputChars = intArg(args, "max_output_chars")
		}
		return agenttool.New(ag, cfg), nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("LongRunningFunctionTool", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		if args == nil {
			return nil, fmt.Errorf("args is nil")
		}
		funcName, ok := args["func"].(string)
		if !ok {
			return nil, fmt.Errorf("func not found in args")
		}
		tool, _, err := ResolveToolReference(ctx, funcName, args)
		if err != nil {
			return nil, err
		}
		if tool == nil {
			return nil, fmt.Errorf("tool '%s' not found", funcName)
		}
		return tool, nil
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolFactory("ExampleTool", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		if args == nil {
			return nil, fmt.Errorf("args is nil")
		}

		raw, ok := args["examples"]
		if !ok {
			return nil, fmt.Errorf("examples not found in args")
		}

		// 1. Cast the top-level 'examples' to a generic slice
		examplesSlice, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("examples is not a list")
		}

		// 2. Iterate and normalize the 'output' field
		for i, item := range examplesSlice {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}

			output := m["output"]
			if output == nil {
				continue
			}

			// Check if 'output' is NOT a slice. If it's a single object,
			// wrap it in a new slice []any{output}
			if _, isSlice := output.([]any); !isSlice {
				m["output"] = []any{output}
				examplesSlice[i] = m
			}
		}

		// 3. Now marshal/unmarshal as usual into your clean struct
		bytes, _ := json.Marshal(examplesSlice)
		var examples []*exampletool.Example
		if err := json.Unmarshal(bytes, &examples); err != nil {
			return nil, fmt.Errorf("failed to decode normalized examples: %w", err)
		}

		return exampletool.New(exampletool.ExampleToolConfig{
			Examples: examples,
		})
	})
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("McpToolset", newMCPToolsetFromConfig)
	if err != nil {
		panic(err)
	}
	err = RegisterToolsetFactory("McpHttpToolset", newMCPToolsetFromConfig)
	if err != nil {
		panic(err)
	}
}

// staticHeaderTransport injects configured HTTP headers into every MCP HTTP request.
type staticHeaderTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *staticHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

func newMCPToolsetFromConfig(ctx context.Context, args map[string]any) (tool.Toolset, error) {
	if args == nil {
		args = map[string]any{}
	}

	// Preferred platform registry format: args.server references
	// adk.yaml -> mcp.servers.<id>. This keeps endpoint and secret headers out
	// of Agent YAML and lets the Builder manage MCP tool bindings by server id.
	if serverID, _ := args["server"].(string); serverID != "" {
		server, ok := runtimeconfig.FromContext(ctx).MCPServer(serverID)
		if !ok {
			return nil, fmt.Errorf("mcp server %q not found in runtime config", serverID)
		}
		merged := map[string]any{}
		for k, v := range args {
			merged[k] = v
		}
		transport := server.Transport
		if transport == "" {
			transport = "streamable_http"
		}
		merged["transport"] = transport
		merged["http_connection_params"] = map[string]any{
			"endpoint": server.Endpoint,
			"headers":  server.Headers,
		}
		if _, ok := merged["tool_filter"]; !ok && len(server.ToolFilter) > 0 {
			merged["tool_filter"] = server.ToolFilter
		}
		if _, ok := merged["require_confirmation"]; !ok {
			merged["require_confirmation"] = server.RequireConfirmation
		}
		args = merged
	}

	transportType, _ := args["transport"].(string)
	if transportType == "" {
		if _, ok := args["http_connection_params"]; ok {
			transportType = "streamable_http"
		} else {
			transportType = "stdio"
		}
	}

	var transport mcp.Transport
	switch transportType {
	case "stdio", "command":
		stdioConnectionParams, ok := args["stdio_connection_params"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("stdio_connection_params not found in args")
		}
		serverParams, ok := stdioConnectionParams["server_params"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("server_params not found in stdio_connection_params")
		}
		command, ok := serverParams["command"].(string)
		if !ok || command == "" {
			return nil, fmt.Errorf("command not found in server_params")
		}
		serverArgsStr := anySliceToStrings(serverParams["args"])
		transport = &mcp.CommandTransport{Command: exec.Command(command, serverArgsStr...)}

	case "streamable_http", "http":
		httpConnectionParams, ok := args["http_connection_params"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("http_connection_params not found in args")
		}
		endpoint, ok := httpConnectionParams["endpoint"].(string)
		if !ok || endpoint == "" {
			return nil, fmt.Errorf("endpoint not found in http_connection_params")
		}
		headers := anyMapToStringMap(httpConnectionParams["headers"])
		client := http.DefaultClient
		if len(headers) > 0 {
			client = &http.Client{
				Transport: &staticHeaderTransport{headers: headers},
			}
		}
		transport = &mcp.StreamableClientTransport{
			Endpoint:   os.ExpandEnv(endpoint),
			HTTPClient: client,
		}

	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", transportType)
	}

	mcpSet, err := mcptoolset.New(mcptoolset.Config{
		Transport:           transport,
		ToolFilter:          optionalToolFilter(args["tool_filter"]),
		RequireConfirmation: boolValue(args["require_confirmation"]),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create mcp toolset: %v", err)
	}
	return mcpSet, nil
}

// NewMCPToolset exposes the platform MCP registry adapter to RuntimePlan
// assemblers. Agent YAML and Hub snapshots use the same server-id based
// contract; keeping construction here avoids a second transport implementation.
func NewMCPToolset(ctx context.Context, args map[string]any) (tool.Toolset, error) {
	return newMCPToolsetFromConfig(ctx, args)
}

func optionalToolFilter(v any) tool.Predicate {
	items := anySliceToStrings(v)
	if len(items) == 0 {
		return nil
	}
	return tool.StringPredicate(items)
}

func anySliceToStrings(v any) []string {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, os.ExpandEnv(item))
		}
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, os.ExpandEnv(s))
			}
		}
		return out
	default:
		return nil
	}
}

func anyMapToStringMap(v any) map[string]string {
	out := map[string]string{}
	switch x := v.(type) {
	case map[string]string:
		for k, val := range x {
			out[k] = os.ExpandEnv(val)
		}
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok {
				out[k] = os.ExpandEnv(s)
			}
		}
	}
	return out
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

// Register allows concrete implementations to add themselves to the system.
// This replaces Python's dynamic importlib logic.
func Register(name string, factory AgentFactory) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		return fmt.Errorf("Register called twice for agent %s", name)
	}
	registry[name] = factory
	return nil
}

// RegisterToolFactory allows concrete implementations to add themselves to the system.
func RegisterToolFactory(name string, factory ToolFactory) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := toolRegistry[name]; dup {
		return fmt.Errorf("RegisterToolFactory called twice for tool %s", name)
	}
	toolRegistry[name] = factory
	return nil
}

func RegisterToolsetFactory(name string, factory ToolsetFactory) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := toolRegistry[name]; dup {
		return fmt.Errorf("RegisterToolsetFactory called twice for toolset %s", name)
	}
	toolRegistry[name] = factory
	return nil
}

func RegisterCallback(name string, callback any) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := callbackRegistry[name]; dup {
		return fmt.Errorf("RegisterCallback called twice for callback %s", name)
	}
	callbackRegistry[name] = callback
	return nil
}

// FromConfig builds an agent from a config file path.
// Equivalent to: def from_config(config_path: str) -> BaseAgent
func FromConfig(ctx context.Context, configPath string) (agent.Agent, error) {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// 1. Read the file
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", absPath)
		}
		return nil, err
	}

	// 2. Peek at the "agent_class" field to know which factory to use.
	var baseConfig baseAgentConfig
	if err := yaml.Unmarshal(data, &baseConfig); err != nil {
		return nil, fmt.Errorf("invalid YAML content: %w", err)
	}

	// Default fallback similar to Python's handling
	agentClass := baseConfig.AgentClass
	if agentClass == "" {
		agentClass = "LlmAgent"
	}

	// 3. Resolve the factory (The Go equivalent of _resolve_agent_class)
	registryMu.RLock()
	factory, exists := registry[agentClass]
	registryMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid agent class '%s': not registered. Ensure the package is imported", agentClass)
	}

	// 4. Delegate creation to the specific factory.
	// We pass the raw data so the factory can unmarshal into its specific Config struct.
	return factory(ctx, data, absPath)
}

func ResolveToolReference(ctx context.Context, toolName string, args map[string]any) (tool.Tool, tool.Toolset, error) {
	if toolName == "" {
		return nil, nil, fmt.Errorf("tool name cannot be empty")
	}

	registryMu.RLock()
	if t, ok := toolRegistry[toolName]; ok {
		registryMu.RUnlock()
		if factory, ok := t.(ToolFactory); ok {
			tool, err := factory(ctx, args)
			return tool, nil, err
		}
		if toolsetFactory, ok := t.(ToolsetFactory); ok {
			toolset, err := toolsetFactory(ctx, args)
			return nil, toolset, err
		}
		return nil, nil, fmt.Errorf("tool '%s' is not a tool or toolset factory", toolName)
	}
	registryMu.RUnlock()
	return nil, nil, fmt.Errorf("tool '%s' not found", toolName)
}

func ResolveCallbackReference(ctx context.Context, callbackName string) (any, error) {
	if callbackName == "" {
		return nil, fmt.Errorf("callback name cannot be empty")
	}

	registryMu.RLock()
	if c, ok := callbackRegistry[callbackName]; ok {
		registryMu.RUnlock()
		return c, nil
	}
	registryMu.RUnlock()
	return nil, fmt.Errorf("callback '%s' not found", callbackName)
}

// ResolveAgentReference builds an agent from a reference config.
func ResolveAgentReference(ctx context.Context, parentPath, refPath string) (agent.Agent, error) {
	if refPath == "" {
		return nil, fmt.Errorf("agent reference path cannot be empty")
	}

	targetPath := refPath
	// Handle relative paths
	if !filepath.IsAbs(refPath) {
		targetPath = filepath.Join(filepath.Dir(parentPath), refPath)
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	registryMu.RLock()
	if a, ok := agentRegistry[absPath]; ok {
		registryMu.RUnlock()
		return a, nil
	}
	registryMu.RUnlock()

	a, err := FromConfig(ctx, absPath)
	if err != nil {
		return nil, err
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if existing, ok := agentRegistry[absPath]; ok {
		return existing, nil
	}
	agentRegistry[absPath] = a
	return a, nil
}

// NewLLMAgent is the factory function registered in the system.
func newLLMAgent(ctx context.Context, data []byte, configPath string) (agent.Agent, error) {
	var cfg llmAgentYAMLConfig

	// Unmarshal parses the shared fields (Name) into BaseAgentConfig
	// AND the specific fields (ModelName) into LLMAgentConfig simultaneously.
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse LLM agent config: %w", err)
	}

	// Validation Logic (Pydantic equivalent)
	if cfg.Name == "" {
		return nil, fmt.Errorf("'name' is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("'model' is required for LlmAgent")
	}

	cfg.ConfigPath = configPath

	agentConfig, err := cfg.toLLMAgentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM agent config: %w", err)
	}

	return llmagent.New(*agentConfig)
}

func newLoopAgent(ctx context.Context, data []byte, configPath string) (agent.Agent, error) {
	var cfg loopAgentYAMLConfig

	// Unmarshal parses the shared fields (Name) into BaseAgentConfig
	// AND the specific fields (ModelName) into LLMAgentConfig simultaneously.
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Loop agent config: %w", err)
	}

	// Validation Logic (Pydantic equivalent)
	if cfg.Name == "" {
		return nil, fmt.Errorf("'name' is required")
	}

	cfg.ConfigPath = configPath

	agentConfig, err := cfg.toLoopAgentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Loop agent config: %w", err)
	}

	return loopagent.New(*agentConfig)
}

func newParallelAgent(ctx context.Context, data []byte, configPath string) (agent.Agent, error) {
	var cfg parallelAgentYAMLConfig

	// Unmarshal parses the shared fields (Name) into BaseAgentConfig
	// AND the specific fields (ModelName) into LLMAgentConfig simultaneously.
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Parallel agent config: %w", err)
	}

	// Validation Logic (Pydantic equivalent)
	if cfg.Name == "" {
		return nil, fmt.Errorf("'name' is required")
	}

	cfg.ConfigPath = configPath

	agentConfig, err := cfg.toParallelAgentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Parallel agent config: %w", err)
	}

	return parallelagent.New(*agentConfig)
}

func newSequentialAgent(ctx context.Context, data []byte, configPath string) (agent.Agent, error) {
	var cfg sequentialAgentYAMLConfig

	// Unmarshal parses the shared fields (Name) into BaseAgentConfig
	// AND the specific fields (ModelName) into LLMAgentConfig simultaneously.
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Sequential agent config: %w", err)
	}

	// Validation Logic (Pydantic equivalent)
	if cfg.Name == "" {
		return nil, fmt.Errorf("'name' is required")
	}

	cfg.ConfigPath = configPath

	agentConfig, err := cfg.toSequentialAgentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Sequential agent config: %w", err)
	}

	return sequentialagent.New(*agentConfig)
}

func boolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	v, _ := args[key].(bool)
	return v
}

func intArg(args map[string]any, key string) int {
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case uint:
		return int(v)
	case uint64:
		return int(v)
	default:
		return 0
	}
}
