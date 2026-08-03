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

package configurable

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/skillservice"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/skilltoolset"
	"google.golang.org/adk/tool/subagenttaskrunnertool"
)

// codeConfig represents a reference to a function or callback.
// Equivalent to: common_configs.codeConfig
type codeConfig struct {
	// Name of the function/method (e.g., "my_pkg.security.Check")
	Name string `yaml:"name"`

	// Optional params if your system supports parameterized callbacks
	Params map[string]any `yaml:"params,omitempty"`
}

// agentRefConfig represents a reference to a sub-agent.
// Equivalent to: common_configs.agentRefConfig
type agentRefConfig struct {
	// Optional stable binding id used by task runners. If omitted, the child
	// agent name is used.
	ID string `yaml:"id,omitempty"`

	// Ref is a friendly agent folder/name reference, e.g.
	// ref: book_chapter_research_worker. It resolves to
	// ../book_chapter_research_worker/root_agent.yaml relative to the parent YAML.
	Ref string `yaml:"ref,omitempty"`

	DisplayName string `yaml:"display_name,omitempty"`
	Role        string `yaml:"role,omitempty"`

	// Path to another agent's YAML file.
	ConfigPath string `yaml:"config_path,omitempty"`

	// OR an inline code reference.
	Code string `yaml:"code,omitempty"`

	// Legacy flat mode. Prefer invocation.mode.
	Mode string `yaml:"mode,omitempty"`

	// Legacy flat execution-policy fields. Prefer invocation/context/output.
	ContextMode       string `yaml:"context_mode,omitempty"`
	SessionKey        string `yaml:"session_key,omitempty"`
	Parallel          bool   `yaml:"parallel,omitempty"`
	ParallelSafe      bool   `yaml:"parallel_safe,omitempty"`
	MaxOutputChars    int    `yaml:"max_output_chars,omitempty"`
	SkipSummarization bool   `yaml:"skip_summarization,omitempty"`

	Invocation *subAgentInvocationConfig `yaml:"invocation,omitempty"`
	Context    *subAgentContextConfig    `yaml:"context,omitempty"`
	Workspace  *subAgentWorkspaceConfig  `yaml:"workspace,omitempty"`
	Output     *subAgentOutputConfig     `yaml:"output,omitempty"`
}

type subAgentInvocationConfig struct {
	Mode           string `yaml:"mode,omitempty"`
	Execution      string `yaml:"execution,omitempty"`
	Async          bool   `yaml:"async,omitempty"`
	Parallel       bool   `yaml:"parallel,omitempty"`
	MaxConcurrency int    `yaml:"max_concurrency,omitempty"`
	TimeoutSeconds int    `yaml:"timeout_seconds,omitempty"`
	Retry          struct {
		MaxAttempts int `yaml:"max_attempts,omitempty"`
	} `yaml:"retry,omitempty"`
}

type subAgentContextConfig struct {
	Mode                    string   `yaml:"mode,omitempty"`
	SessionKey              string   `yaml:"session_key,omitempty"`
	InheritParentMessages   bool     `yaml:"inherit_parent_messages,omitempty"`
	InheritProjectArtifacts bool     `yaml:"inherit_project_artifacts,omitempty"`
	PassStateKeys           []string `yaml:"pass_state_keys,omitempty"`
}

type subAgentWorkspaceConfig struct {
	Mode           string `yaml:"mode,omitempty"`
	Base           string `yaml:"base,omitempty"`
	CommitToParent bool   `yaml:"commit_to_parent,omitempty"`
}

type subAgentOutputConfig struct {
	Mode                string `yaml:"mode,omitempty"`
	MaxChars            int    `yaml:"max_chars,omitempty"`
	ForbidRawSourceText bool   `yaml:"forbid_raw_source_text,omitempty"`
}

type ToolConfig struct {
	// Type is optional. When set to "mcp", the tool entry references a
	// platform-level MCP server registry entry instead of naming a concrete
	// builtin/function tool.
	Type string `yaml:"type,omitempty"`

	// Name of the tool/method (e.g., "my_pkg.security.Check").
	Name string `yaml:"name,omitempty"`

	// Server is the MCP server id when Type == "mcp".
	Server string `yaml:"server,omitempty"`

	// Namespace is reserved for future tool-name namespacing.
	Namespace string `yaml:"namespace,omitempty"`

	// ToolFilter limits which remote MCP tools are exposed to the model.
	ToolFilter []string `yaml:"tool_filter,omitempty"`

	RequireConfirmation bool `yaml:"require_confirmation,omitempty"`

	// Optional params if your system supports parameterized callbacks
	Args map[string]any `yaml:"args,omitempty"`
}

// baseAgentConfig matches the Python baseAgentConfig Pydantic model.
//
// Usage: Do not use this struct directly for unmarshalling specific agents.
// Embed it into concrete agent configs (see Example below).
type baseAgentConfig struct {
	// Required. The class of the agent.
	// Default is "BaseAgent" in Python, but usually overridden by concrete agents.
	AgentClass string `yaml:"agent_class"`

	// Required. The name of the agent.
	Name string `yaml:"name"`

	// Optional. Description of the agent.
	Description string `yaml:"description,omitempty"`

	// Optional. List of sub-agents.
	SubAgents []agentRefConfig `yaml:"sub_agents,omitempty"`

	// Optional. Callbacks to run before execution.
	BeforeAgentCallbacks []codeConfig `yaml:"before_agent_callbacks,omitempty"`

	// Optional. Callbacks to run after execution.
	AfterAgentCallbacks []codeConfig `yaml:"after_agent_callbacks,omitempty"`

	// Path to the config file.
	ConfigPath string `yaml:"-"`

	// Handle extra fields (extra='allow'):
	// If you use this struct standalone, this map catches unknown fields.
	// However, the preferred pattern is to embed this struct in a concrete config
	// so specific fields are strongly typed.
	AdditionalProperties map[string]any `yaml:",inline"`
}

// llmAgentYAMLConfig is the concrete config for a specific agent.
type llmAgentYAMLConfig struct {
	// 1. Embed baseAgentConfig with ",inline".
	// This pulls "name", "sub_agents", etc. to the top level of the YAML.
	baseAgentConfig `yaml:",inline"`

	// 2. Define the "extra" fields specific to this agent here.
	Model string `yaml:"model"`

	Instruction string `yaml:"instruction"`

	Tools []ToolConfig `yaml:"tools,omitempty"`

	// Skills are ADK SKILL.md folders exposed to this LlmAgent through
	// skilltoolset. Values must match skill frontmatter/directory names.
	Skills []string `yaml:"skills,omitempty"`

	DisallowTransferToPeers bool `yaml:"disallow_transfer_to_peers,omitempty"`

	DisallowTransferToParent bool `yaml:"disallow_transfer_to_parent,omitempty"`

	GenerateContentConfig *genai.GenerateContentConfig `yaml:"generate_content_config,omitempty"`
}

func (c *llmAgentYAMLConfig) toLLMAgentConfig(ctx context.Context) (*llmagent.Config, error) {
	llm, resolvedModelID, err := runtimeconfig.NewModel(ctx, c.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve/create model %q for agent %q: %w", c.Model, c.Name, err)
	}
	fmt.Printf("🧠 Agent %s model ref %q resolved to spec %q concrete model %q\n", c.Name, c.Model, resolvedModelID, llm.Name())
	generationConfig := runtimeconfig.MergeGenerateContentConfig(
		runtimeconfig.FromContext(ctx).DefaultGenerateContentConfigFor(resolvedModelID),
		c.GenerateContentConfig,
	)

	subAgents, subAgentTools, subAgentToolsets, err := resolveLLMSubAgents(ctx, c.ConfigPath, c.SubAgents)
	if err != nil {
		return nil, err
	}

	tools, toolsets, err := resolveTools(ctx, c.ConfigPath, c.Tools)
	if err != nil {
		return nil, err
	}
	tools = append(tools, subAgentTools...)
	toolsets = append(toolsets, subAgentToolsets...)

	skillToolsets, err := resolveSkillToolsets(ctx, c.Skills)
	if err != nil {
		return nil, err
	}
	toolsets = append(toolsets, skillToolsets...)

	beforeCallbacks, err := resolveCallbacks[agent.BeforeAgentCallback](ctx, c.BeforeAgentCallbacks)
	if err != nil {
		return nil, err
	}

	afterCallbacks, err := resolveCallbacks[agent.AfterAgentCallback](ctx, c.AfterAgentCallbacks)
	if err != nil {
		return nil, err
	}

	return &llmagent.Config{
		Name:                     c.Name,
		Description:              c.Description,
		SubAgents:                subAgents,
		Model:                    llm,
		Instruction:              c.Instruction,
		DisallowTransferToPeers:  c.DisallowTransferToPeers,
		DisallowTransferToParent: c.DisallowTransferToParent,
		Tools:                    tools,
		Toolsets:                 toolsets,
		GenerateContentConfig:    generationConfig,
		BeforeAgentCallbacks:     beforeCallbacks,
		AfterAgentCallbacks:      afterCallbacks,
	}, nil
}

type loopAgentYAMLConfig struct {
	baseAgentConfig `yaml:",inline"`
	MaxIterations   uint `yaml:"max_iterations"`
}

func (c *loopAgentYAMLConfig) toLoopAgentConfig(ctx context.Context) (*loopagent.Config, error) {
	subAgents, err := resolveSubAgents(ctx, c.ConfigPath, c.SubAgents)
	if err != nil {
		return nil, err
	}

	return &loopagent.Config{
		AgentConfig: agent.Config{
			Name:        c.Name,
			Description: c.Description,
			SubAgents:   subAgents,
		},
		MaxIterations: c.MaxIterations,
	}, nil
}

// ParallelAgentYAMLConfig is the concrete config for a specific agent.
type parallelAgentYAMLConfig struct {
	baseAgentConfig `yaml:",inline"`
}

func (c *parallelAgentYAMLConfig) toParallelAgentConfig(ctx context.Context) (*parallelagent.Config, error) {
	subAgents, err := resolveSubAgents(ctx, c.ConfigPath, c.SubAgents)
	if err != nil {
		return nil, err
	}

	return &parallelagent.Config{
		AgentConfig: agent.Config{
			Name:        c.Name,
			Description: c.Description,
			SubAgents:   subAgents,
		},
	}, nil
}

// SequentialAgentYAMLConfig is the concrete config for a specific agent.
type sequentialAgentYAMLConfig struct {
	baseAgentConfig `yaml:",inline"`
}

func (c *sequentialAgentYAMLConfig) toSequentialAgentConfig(ctx context.Context) (*sequentialagent.Config, error) {
	subAgents, err := resolveSubAgents(ctx, c.ConfigPath, c.SubAgents)
	if err != nil {
		return nil, err
	}

	return &sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        c.Name,
			Description: c.Description,
			SubAgents:   subAgents,
		},
	}, nil
}

func resolveLLMSubAgents(ctx context.Context, parentPath string, refs []agentRefConfig) ([]agent.Agent, []tool.Tool, []tool.Toolset, error) {
	var agents []agent.Agent
	var tools []tool.Tool
	var toolsets []tool.Toolset
	var taskWorkers []subagenttaskrunnertool.WorkerConfig

	for _, ref := range refs {
		ag, err := resolveSubAgentReference(ctx, parentPath, ref)
		if err != nil {
			return nil, nil, nil, err
		}
		if ag == nil {
			continue
		}

		mode := subAgentInvocationMode(ref)
		switch mode {
		case "task", "tasks", "tool", "agent_tool", "isolated", "worker":
			// Task mode means: expose the child through the unified task runner.
			// The controller stays the user-facing agent; child results return as refs/summary.
			cfg := subAgentWorkerConfig(ref, ag)
			taskWorkers = append(taskWorkers, cfg)
		case "transfer", "sub_agent", "handoff":
			// Handoff mode keeps the native ADK transfer_to_agent behavior.
			agents = append(agents, ag)
		default:
			return nil, nil, nil, fmt.Errorf("unsupported sub_agent invocation mode %q for %s", mode, firstNonEmptyString(ref.ConfigPath, ref.Ref, ref.Code))
		}
	}

	if len(taskWorkers) > 0 {
		ts, err := subagenttaskrunnertool.NewToolset(taskWorkers)
		if err != nil {
			return nil, nil, nil, err
		}
		toolsets = append(toolsets, ts)
	}
	return agents, tools, toolsets, nil
}

func subAgentInvocationMode(ref agentRefConfig) string {
	mode := ""
	if ref.Invocation != nil {
		mode = strings.ToLower(strings.TrimSpace(ref.Invocation.Mode))
	}
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(ref.Mode))
	}
	if mode == "" {
		// Backward compatibility: flat execution fields imply task/tool mode.
		if ref.ContextMode != "" || ref.SessionKey != "" || ref.Parallel || ref.ParallelSafe || ref.MaxOutputChars > 0 || ref.SkipSummarization || ref.Context != nil || ref.Workspace != nil || ref.Output != nil {
			mode = "task"
		} else {
			mode = "transfer"
		}
	}
	return mode
}

func subAgentWorkerConfig(ref agentRefConfig, ag agent.Agent) subagenttaskrunnertool.WorkerConfig {
	contextMode := strings.TrimSpace(ref.ContextMode)
	if ref.Context != nil && strings.TrimSpace(ref.Context.Mode) != "" {
		contextMode = strings.TrimSpace(ref.Context.Mode)
	}
	sessionKey := strings.TrimSpace(ref.SessionKey)
	if ref.Context != nil && strings.TrimSpace(ref.Context.SessionKey) != "" {
		sessionKey = strings.TrimSpace(ref.Context.SessionKey)
	}
	parallelSafe := ref.Parallel || ref.ParallelSafe
	defaultConcurrency := 1
	if ref.Invocation != nil {
		if ref.Invocation.Parallel || strings.EqualFold(ref.Invocation.Execution, "parallel") {
			parallelSafe = true
		}
		if ref.Invocation.MaxConcurrency > 0 {
			defaultConcurrency = ref.Invocation.MaxConcurrency
		} else if ref.Invocation.Parallel {
			defaultConcurrency = 2
		}
	}
	maxOutputChars := ref.MaxOutputChars
	if ref.Output != nil && ref.Output.MaxChars > 0 {
		maxOutputChars = ref.Output.MaxChars
	}
	id := strings.TrimSpace(ref.ID)
	if id == "" {
		id = strings.TrimSpace(ref.Ref)
	}
	if id == "" {
		id = ag.Name()
	}
	return subagenttaskrunnertool.WorkerConfig{
		ID:                 id,
		DisplayName:        strings.TrimSpace(ref.DisplayName),
		Role:               strings.TrimSpace(ref.Role),
		Agent:              ag,
		ContextMode:        contextMode,
		SessionKey:         sessionKey,
		ParallelSafe:       parallelSafe,
		MaxOutputChars:     maxOutputChars,
		DefaultConcurrency: defaultConcurrency,
		SkipSummarization:  ref.SkipSummarization,
	}
}

func resolveSubAgentReference(ctx context.Context, parentPath string, ref agentRefConfig) (agent.Agent, error) {
	if ref.ConfigPath != "" {
		a, err := ResolveAgentReference(ctx, parentPath, ref.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve agent reference %s: %w", ref.ConfigPath, err)
		}
		return a, nil
	}
	if strings.TrimSpace(ref.Ref) != "" {
		configPath := filepath.Join("..", strings.TrimSpace(ref.Ref), "root_agent.yaml")
		a, err := ResolveAgentReference(ctx, parentPath, configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve agent ref %s via %s: %w", ref.Ref, configPath, err)
		}
		return a, nil
	}
	if ref.Code != "" {
		return nil, fmt.Errorf("inline code agent references are not yet supported for %s", ref.Code)
	}
	return nil, nil
}

func resolveSubAgents(ctx context.Context, parentPath string, refs []agentRefConfig) ([]agent.Agent, error) {
	var agents []agent.Agent
	for _, ref := range refs {
		a, err := resolveSubAgentReference(ctx, parentPath, ref)
		if err != nil {
			return nil, err
		}
		if a != nil {
			agents = append(agents, a)
		}
	}
	return agents, nil
}

type contextKey string

const parentPathKey contextKey = "parentPath"

func resolveTools(ctx context.Context, parentPath string, toolConfigs []ToolConfig) ([]tool.Tool, []tool.Toolset, error) {
	var tools []tool.Tool
	var toolsets []tool.Toolset
	for _, tc := range toolConfigs {
		toolName := tc.Name
		args := cloneArgs(tc.Args)

		// Friendly Agent Builder MCP formats. Keep them all supported because
		// different UI saves may produce slightly different YAML shapes:
		//
		//   - type: mcp
		//     server: novel_assets
		//     tool_filter: [list_split_books]
		//
		//   - name: McpHttpToolset
		//     server: novel_assets
		//     tool_filter: [list_split_books]
		//
		//   - name: McpHttpToolset
		//     args:
		//       server: novel_assets
		if tc.Type == "mcp" || toolName == "McpHttpToolset" {
			toolName = "McpHttpToolset"
			server := firstNonEmptyString(tc.Server, stringArg(args, "server"), tc.Name)
			if tc.Type != "mcp" && tc.Name == "McpHttpToolset" {
				server = firstNonEmptyString(tc.Server, stringArg(args, "server"))
			}

			// If args already contains raw http_connection_params or stdio params, keep
			// it as an advanced low-level MCP Toolset config. Otherwise it must refer to
			// an mcp.servers.<id> registry entry.
			_, hasHTTPParams := args["http_connection_params"]
			_, hasStdioParams := args["stdio_connection_params"]
			if !hasHTTPParams && !hasStdioParams {
				if server == "" {
					return nil, nil, fmt.Errorf("mcp tool entry requires server or args.http_connection_params")
				}
				args["server"] = server
			}

			if tc.Namespace != "" {
				args["namespace"] = tc.Namespace
			}
			if len(tc.ToolFilter) > 0 {
				args["tool_filter"] = tc.ToolFilter
			}
			if tc.RequireConfirmation {
				args["require_confirmation"] = true
			}
		}

		if toolName != "" {
			ctx = context.WithValue(ctx, parentPathKey, parentPath)
			a, ts, err := ResolveToolReference(ctx, toolName, args)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to resolve tool reference %s: %w", toolName, err)
			}
			if a != nil {
				tools = append(tools, a)
			}
			if ts != nil {
				toolsets = append(toolsets, ts)
			}
		}
	}
	return tools, toolsets, nil
}

func cloneArgs(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func firstNonEmptyString(items ...string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return strings.TrimSpace(item)
		}
	}
	return ""
}

func resolveSkillToolsets(ctx context.Context, selected []string) ([]tool.Toolset, error) {
	if len(selected) == 0 {
		return nil, nil
	}
	cfg := runtimeconfig.FromContext(ctx)
	if !cfg.Skills.Enabled {
		return nil, fmt.Errorf("skills are configured on agent but runtime skills.enabled is false")
	}
	svc, err := skillservice.NewFileSystemService(cfg.Skills.Root)
	if err != nil {
		return nil, fmt.Errorf("create skill service: %w", err)
	}
	source, err := svc.Source(ctx, selected, cfg.Skills.Preload)
	if err != nil {
		return nil, fmt.Errorf("create skill source: %w", err)
	}
	frontmatters, err := source.ListFrontmatters(ctx)
	if err != nil {
		return nil, fmt.Errorf("load selected skill frontmatters: %w", err)
	}
	if len(frontmatters) == 0 {
		return nil, fmt.Errorf("no selected skills were found: %v", selected)
	}
	ts, err := skilltoolset.New(ctx, skilltoolset.Config{Source: source})
	if err != nil {
		return nil, fmt.Errorf("create skill toolset: %w", err)
	}
	return []tool.Toolset{ts}, nil
}

func resolveCallbacks[T any](ctx context.Context, callbacks []codeConfig) ([]T, error) {
	var cbs []T
	for _, ref := range callbacks {
		if ref.Name != "" {
			c, err := ResolveCallbackReference(ctx, ref.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve callback reference %s: %w", ref.Name, err)
			}
			cb, ok := c.(T)
			if !ok {
				return nil, fmt.Errorf("callback %s is of type %T and not %T", ref.Name, c, *new(T))
			}
			cbs = append(cbs, cb)
		}
	}
	return cbs, nil
}
