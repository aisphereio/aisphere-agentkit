// Copyright 2025 Google LLC
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

// Package agenttool provides a tool that allows an agent to call another agent.
// This enables composition of agents, which can be useful for scenarios where
// different types of `genai` tools cannot be used together.
package agenttool

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/llminternal"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// agentTool implements a tool that allows an agent to call another agent.
type agentTool struct {
	agent             agent.Agent
	skipSummarization bool

	// ContextMode controls how the wrapped sub-agent session is created.
	//   inherit        (default): create a new sub-agent session and copy parent state.
	//   fresh          : create a new sub-agent session with an empty state.
	//   sticky         : reuse one sub-agent session per user/session_key without copying parent state.
	//   sticky_inherit : reuse one sub-agent session per user/session_key; copy parent state only when first created.
	ContextMode string
	// SessionKey is used by sticky modes to separate multiple logical child contexts.
	SessionKey string
	// ParallelSafe indicates that calls to this AgentTool may run concurrently.
	// Keep false for sticky/contextful workers; set true for stateless batch workers.
	ParallelSafe bool
	// MaxOutputChars truncates the sub-agent response returned to the parent agent.
	// The sub-agent should save large results as artifacts and return only refs.
	MaxOutputChars int

	runMu sync.Mutex

	stickyMu       sync.Mutex
	stickySessions map[string]string
	stickyService  session.Service
	stickyArtifact artifact.Service
	stickyMemory   memory.Service
}

// Config holds the configuration for an agent tool.
type Config struct {
	// SkipSummarization, if true, will cause the agent to skip summarization
	// after the sub-agent finishes execution.
	SkipSummarization bool

	// ContextMode controls the child session strategy. Supported values:
	// inherit, fresh, sticky, sticky_inherit.
	ContextMode string
	// SessionKey is used by sticky modes to reuse a named child session.
	SessionKey string
	// ParallelSafe allows concurrent calls to this AgentTool instance. For
	// sticky/contextful workers keep this false unless the child agent is designed
	// for concurrent access.
	ParallelSafe bool
	// MaxOutputChars truncates the text returned to the parent agent. Full worker
	// outputs should be saved as artifacts/tool assets and returned by reference.
	MaxOutputChars int
}

// New creates a new agent tool.
// If cfg is nil, it defaults to an inherited fresh child session, matching the
// original ADK AgentTool behavior.
func New(agent agent.Agent, cfg *Config) tool.Tool {
	if cfg == nil {
		cfg = &Config{}
	}
	contextMode := strings.TrimSpace(cfg.ContextMode)
	if contextMode == "" {
		contextMode = "inherit"
	}
	return &agentTool{
		agent:             agent,
		skipSummarization: cfg.SkipSummarization,
		ContextMode:       contextMode,
		SessionKey:        strings.TrimSpace(cfg.SessionKey),
		ParallelSafe:      cfg.ParallelSafe,
		MaxOutputChars:    cfg.MaxOutputChars,
		stickySessions:    map[string]string{},
		stickyService:     session.InMemoryService(),
		stickyArtifact:    artifact.InMemoryService(),
		stickyMemory:      memory.InMemoryService(),
	}
}

// Name implements tool.Tool.
func (t *agentTool) Name() string {
	return t.agent.Name()
}

// Description implements tool.Tool.
func (t *agentTool) Description() string {
	return t.agent.Description()
}

// IsLongRunning implements tool.Tool.
func (t *agentTool) IsLongRunning() bool {
	return false
}

// Declaration returns the function declaration for the wrapped agent.
// It generates a function declaration based on the agent's input schema.
// If the agent does not have an input schema, a default schema with a
// "request" string parameter is used.
func (t *agentTool) Declaration() *genai.FunctionDeclaration {
	decl := &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
	}

	var agentInputSchema *genai.Schema
	llmAgent, ok := t.agent.(llminternal.Agent)
	if ok && llmAgent != nil {
		// TODO - understand what build_function_declaration does in python and apply if needed.
		internalLlmAgent, ok := t.agent.(llminternal.Agent)
		if !ok {
			return nil
		}
		agentInputSchema = llminternal.Reveal(internalLlmAgent).InputSchema
	}

	if agentInputSchema != nil {
		decl.Parameters = agentInputSchema
	} else {
		decl.Parameters = &genai.Schema{
			Type: "OBJECT",
			Properties: map[string]*genai.Schema{
				"request": {Type: "STRING"},
			},
			Required: []string{"request"},
		}
	}
	// TODO - understand how _api_variant affects response type.
	return decl
}

// Run executes the wrapped agent with the provided arguments.
// It creates or reuses a sub-agent session according to Config.ContextMode,
// runs the child agent, and returns the final result.
func (t *agentTool) Run(toolCtx tool.Context, args any) (map[string]any, error) {
	if !t.ParallelSafe {
		t.runMu.Lock()
		defer t.runMu.Unlock()
	}

	margs, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("agentTool expects map[string]any arguments, got %T", args)
	}
	traceMeta := extractSubAgentTraceMeta(margs)

	if t.skipSummarization {
		if actions := toolCtx.Actions(); actions != nil {
			actions.SkipSummarization = true
		}
	}

	var agentInputSchema *genai.Schema
	llmAgent, ok := t.agent.(llminternal.Agent)
	isLllmAgent := (ok && llmAgent != nil)
	if isLllmAgent {
		internalLlmAgent, ok := t.agent.(llminternal.Agent)
		if !ok {
			return nil, fmt.Errorf("internal error: failed to convert to llm agent")
		}
		agentInputSchema = llminternal.Reveal(internalLlmAgent).InputSchema
	}

	var content *genai.Content
	var err error
	if agentInputSchema != nil {
		if err = utils.ValidateMapOnSchema(margs, agentInputSchema, true); err != nil {
			return nil, fmt.Errorf("argument validation failed for agent %s: %w", t.agent.Name(), err)
		}
		jsonData, err := json.Marshal(margs)
		if err != nil {
			return nil, fmt.Errorf("error serializing tool arguments for agent %s: %w", t.agent.Name(), err)
		}
		content = genai.NewContentFromText(string(jsonData), genai.RoleUser)
	} else {
		input, ok := margs["request"]
		if !ok {
			return nil, fmt.Errorf("missing required argument 'request' for agent %s", t.agent.Name())
		}
		inputText, ok := input.(string)
		if !ok {
			// Try to convert to string if not already one
			inputText = fmt.Sprint(input)
		}
		content = genai.NewContentFromText(inputText, genai.RoleUser)
	}

	sessionService, artifactService, memoryService := t.servicesForMode()

	r, err := runner.New(runner.Config{
		AppName:         t.agent.Name(),
		Agent:           t.agent,
		SessionService:  sessionService,
		ArtifactService: artifactService,
		MemoryService:   memoryService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner")
	}

	stateMap := t.initialState(toolCtx)

	subSession, err := t.getOrCreateSubSession(toolCtx, sessionService, stateMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create session for sub-agent %s: %w", t.agent.Name(), err)
	}
	t.recordSubAgentSessionCreated(toolCtx, traceMeta, subSession)
	t.recordSubAgentPrompt(toolCtx, traceMeta, content)

	// TODO(dpasiukevich): verify agent loop termination.
	eventCh := r.Run(toolCtx, subSession.Session.UserID(), subSession.Session.ID(), content, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	})

	var lastEvent *session.Event
	for event, err := range eventCh {
		if err != nil {
			t.recordSubAgentSessionDisposed(toolCtx, traceMeta, subSession, "failed", err.Error())
			return nil, fmt.Errorf("error during execution of sub-agent %s: %w", t.agent.Name(), err)
		}
		t.recordSubAgentChildEvent(toolCtx, traceMeta, subSession, event)
		if event.ErrorCode != "" || event.ErrorMessage != "" {
			err := fmt.Errorf("error from sub-agent %q (code: %q, message: %q)", t.agent.Name(), event.ErrorCode, event.ErrorMessage)
			t.recordSubAgentSessionDisposed(toolCtx, traceMeta, subSession, "failed", err.Error())
			return nil, err
		}
		if event.LLMResponse.Content != nil {
			lastEvent = event
		}
	}
	t.recordSubAgentSessionDisposed(toolCtx, traceMeta, subSession, "completed", "")

	if lastEvent == nil {
		return map[string]any{}, nil
	}

	lastContent := lastEvent.LLMResponse.Content
	var textParts []string
	for _, part := range lastContent.Parts {
		if part != nil && part.Text != "" {
			textParts = append(textParts, part.Text)
		}
	}
	outputText := strings.Join(textParts, "\n")
	outputText = truncateRunes(outputText, t.MaxOutputChars)

	if outputText == "" {
		return map[string]any{}, nil
	}
	if isLllmAgent {
		internalLlmAgent, ok := t.agent.(llminternal.Agent)
		if !ok {
			return nil, fmt.Errorf("internal error: failed to convert to llm agent")
		}
		if agentOutputSchema := llminternal.Reveal(internalLlmAgent).OutputSchema; agentOutputSchema != nil {
			// Assuming schemautils.ValidateOutputSchema parses the JSON string outputText
			// and validates it against the agentOutputSchema, returning a map[string]any.
			parsedOutput, err := utils.ValidateOutputSchema(outputText, agentOutputSchema)
			if err != nil {
				return nil, fmt.Errorf("output validation failed for sub-agent %s: %w", t.agent.Name(), err)
			}
			return parsedOutput, nil
		}
	}

	return map[string]any{"result": outputText}, nil
}

func (t *agentTool) servicesForMode() (session.Service, artifact.Service, memory.Service) {
	if strings.HasPrefix(t.ContextMode, "sticky") {
		return t.stickyService, t.stickyArtifact, t.stickyMemory
	}
	return session.InMemoryService(), artifact.InMemoryService(), memory.InMemoryService()
}

func (t *agentTool) initialState(toolCtx tool.Context) map[string]any {
	stateMap := make(map[string]any)
	if t.ContextMode == "fresh" || t.ContextMode == "sticky" {
		return stateMap
	}
	for k, v := range toolCtx.State().All() {
		// Filter out adk internal states.
		if !strings.HasPrefix(k, "_adk") {
			stateMap[k] = v
		}
	}
	return stateMap
}

func (t *agentTool) getOrCreateSubSession(toolCtx tool.Context, sessionService session.Service, stateMap map[string]any) (*session.CreateResponse, error) {
	if !strings.HasPrefix(t.ContextMode, "sticky") {
		return sessionService.Create(toolCtx, &session.CreateRequest{AppName: t.agent.Name(), UserID: toolCtx.UserID(), State: stateMap})
	}

	key := t.stickyKey(toolCtx)
	t.stickyMu.Lock()
	defer t.stickyMu.Unlock()
	if sessionID, ok := t.stickySessions[key]; ok && sessionID != "" {
		if got, err := sessionService.Get(toolCtx, &session.GetRequest{AppName: t.agent.Name(), UserID: toolCtx.UserID(), SessionID: sessionID}); err == nil {
			return &session.CreateResponse{Session: got.Session}, nil
		}
	}
	resp, err := sessionService.Create(toolCtx, &session.CreateRequest{AppName: t.agent.Name(), UserID: toolCtx.UserID(), State: stateMap})
	if err != nil {
		return nil, err
	}
	t.stickySessions[key] = resp.Session.ID()
	return resp, nil
}

func (t *agentTool) stickyKey(toolCtx tool.Context) string {
	key := t.SessionKey
	if key == "" {
		key = t.agent.Name()
	}
	return toolCtx.UserID() + "|" + t.agent.Name() + "|" + key
}

func truncateRunes(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	return string(r[:maxChars]) + "\n\n[truncated_by_agent_tool: output exceeded max_output_chars; worker should save full result as artifact and return references]"
}

// ProcessRequest adds the agent tool's function declaration to the LLM request.
func (t *agentTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return toolutils.PackTool(req, t)
}

func extractSubAgentTraceMeta(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	raw, ok := args["__adk_subagent_trace"]
	if !ok {
		return nil
	}
	delete(args, "__adk_subagent_trace")
	if raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	if m, ok := raw.(map[string]string); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return map[string]any{"trace_meta": raw}
}

func subAgentTraceEnabled(meta map[string]any) bool {
	return len(meta) > 0
}

func mergeSubAgentTraceData(meta map[string]any, data map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range meta {
		out[k] = v
	}
	for k, v := range data {
		out[k] = v
	}
	return out
}

func (t *agentTool) recordSubAgentSessionCreated(toolCtx tool.Context, meta map[string]any, subSession *session.CreateResponse) {
	if !subAgentTraceEnabled(meta) || subSession == nil || subSession.Session == nil {
		return
	}
	runtimetrace.Record(toolCtx, runtimetrace.EventSubAgentTaskSessionCreated, mergeSubAgentTraceData(meta, map[string]any{
		"child_app_name":   subSession.Session.AppName(),
		"child_session_id": subSession.Session.ID(),
		"child_user_id":    subSession.Session.UserID(),
		"context_mode":     t.ContextMode,
		"session_key":      t.SessionKey,
		"ephemeral":        !strings.HasPrefix(t.ContextMode, "sticky"),
	}))
}

func (t *agentTool) recordSubAgentPrompt(toolCtx tool.Context, meta map[string]any, content *genai.Content) {
	if !subAgentTraceEnabled(meta) {
		return
	}
	runtimetrace.Record(toolCtx, runtimetrace.EventSubAgentTaskPrompt, mergeSubAgentTraceData(meta, map[string]any{
		"child_agent": t.agent.Name(),
		"content":     summarizeTraceContent(content),
		"text":        strings.Join(utils.TextParts(content), "\n"),
	}))
}

func (t *agentTool) recordSubAgentChildEvent(toolCtx tool.Context, meta map[string]any, subSession *session.CreateResponse, ev *session.Event) {
	if !subAgentTraceEnabled(meta) || ev == nil {
		return
	}
	kind := "event"
	calls := summarizeTraceFunctionCalls(utils.FunctionCalls(ev.LLMResponse.Content))
	responses := summarizeTraceFunctionResponses(utils.FunctionResponses(ev.LLMResponse.Content))
	text := strings.Join(utils.TextParts(ev.LLMResponse.Content), "\n")
	if len(calls) > 0 {
		kind = "tool_call"
	} else if len(responses) > 0 {
		kind = "tool_result"
	} else if text != "" {
		kind = "model_response"
	}
	data := map[string]any{
		"kind":                kind,
		"child_agent":         t.agent.Name(),
		"child_invocation_id": ev.InvocationID,
		"child_author":        ev.Author,
		"child_branch":        ev.Branch,
		"event_id":            ev.ID,
		"partial":             ev.LLMResponse.Partial,
		"turn_complete":       ev.LLMResponse.TurnComplete,
		"is_final_response":   ev.IsFinalResponse(),
		"model_version":       ev.LLMResponse.ModelVersion,
		"finish_reason":       fmt.Sprint(ev.LLMResponse.FinishReason),
		"text":                text,
		"content":             summarizeTraceContent(ev.LLMResponse.Content),
		"function_calls":      calls,
		"function_responses":  responses,
		"usage":               ev.LLMResponse.UsageMetadata,
	}
	if ev.ErrorCode != "" || ev.ErrorMessage != "" {
		data["error_code"] = ev.ErrorCode
		data["error_message"] = ev.ErrorMessage
	}
	if subSession != nil && subSession.Session != nil {
		data["child_app_name"] = subSession.Session.AppName()
		data["child_session_id"] = subSession.Session.ID()
	}
	runtimetrace.Record(toolCtx, runtimetrace.EventSubAgentTaskChildEvent, mergeSubAgentTraceData(meta, data))
}

func (t *agentTool) recordSubAgentSessionDisposed(toolCtx tool.Context, meta map[string]any, subSession *session.CreateResponse, status string, errText string) {
	if !subAgentTraceEnabled(meta) {
		return
	}
	data := map[string]any{
		"child_agent": t.agent.Name(),
		"status":      status,
		"ephemeral":   !strings.HasPrefix(t.ContextMode, "sticky"),
	}
	if errText != "" {
		data["error"] = errText
	}
	if subSession != nil && subSession.Session != nil {
		data["child_app_name"] = subSession.Session.AppName()
		data["child_session_id"] = subSession.Session.ID()
	}
	runtimetrace.Record(toolCtx, runtimetrace.EventSubAgentTaskSessionDisposed, mergeSubAgentTraceData(meta, data))
}

func summarizeTraceContent(content *genai.Content) map[string]any {
	if content == nil {
		return nil
	}
	parts := make([]map[string]any, 0, len(content.Parts))
	for _, part := range content.Parts {
		parts = append(parts, summarizeTracePart(part))
	}
	return map[string]any{"role": content.Role, "parts": parts}
}

func summarizeTracePart(part *genai.Part) map[string]any {
	if part == nil {
		return nil
	}
	out := map[string]any{}
	if part.Text != "" {
		out["text"] = part.Text
	}
	if part.FunctionCall != nil {
		out["function_call"] = map[string]any{
			"id":   part.FunctionCall.ID,
			"name": part.FunctionCall.Name,
			"args": part.FunctionCall.Args,
		}
	}
	if part.FunctionResponse != nil {
		out["function_response"] = map[string]any{
			"id":       part.FunctionResponse.ID,
			"name":     part.FunctionResponse.Name,
			"response": part.FunctionResponse.Response,
		}
	}
	if part.InlineData != nil {
		out["inline_data"] = map[string]any{"mime_type": part.InlineData.MIMEType, "bytes": len(part.InlineData.Data)}
	}
	if part.FileData != nil {
		out["file_data"] = part.FileData
	}
	if len(out) == 0 {
		b, _ := json.Marshal(part)
		out["raw"] = string(b)
	}
	return out
}

func summarizeTraceFunctionCalls(calls []*genai.FunctionCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		if call == nil {
			continue
		}
		out = append(out, map[string]any{"id": call.ID, "name": call.Name, "args": call.Args})
	}
	return out
}

func summarizeTraceFunctionResponses(responses []*genai.FunctionResponse) []map[string]any {
	out := make([]map[string]any, 0, len(responses))
	for _, resp := range responses {
		if resp == nil {
			continue
		}
		out = append(out, map[string]any{"id": resp.ID, "name": resp.Name, "response": resp.Response})
	}
	return out
}
