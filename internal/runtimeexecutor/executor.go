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

// Package runtimeexecutor runs Hub-authorized RuntimePlans through ADK-Go.
package runtimeexecutor

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/agentassembler"
	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/permissiongate"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

var (
	ErrSkillContextToolFailed = errors.New("skill context tool failed")
	ErrToolExecutionFailed    = errors.New("tool execution failed")
)

// EventError turns non-transport failures embedded in ADK events into Runtime
// execution failures. ADK intentionally feeds function errors back to the LLM,
// but the Runtime ledger must not mark a run successful when authorization was
// denied or a Skill context reader failed.
func EventError(event *session.Event) error {
	if event == nil {
		return nil
	}
	if code := strings.TrimSpace(event.LLMResponse.ErrorCode); code != "" {
		return fmt.Errorf("model event failed [%s]: %s", code, strings.TrimSpace(event.LLMResponse.ErrorMessage))
	}
	if message := strings.TrimSpace(event.LLMResponse.ErrorMessage); message != "" {
		return fmt.Errorf("model event failed: %s", message)
	}
	if event.LLMResponse.Content == nil {
		return nil
	}
	for _, part := range event.LLMResponse.Content.Parts {
		if part == nil || part.FunctionResponse == nil {
			continue
		}
		responseError := responseErrorMessage(part.FunctionResponse.Response)
		if responseError == "" {
			continue
		}
		if strings.Contains(strings.ToLower(responseError), permissiongate.ErrToolDenied.Error()) {
			return fmt.Errorf("%w: %s", permissiongate.ErrToolDenied, responseError)
		}
		switch strings.TrimSpace(part.FunctionResponse.Name) {
		case "list_skills", "load_skill", "load_skill_resource":
			return fmt.Errorf("%w: %s: %s", ErrSkillContextToolFailed, part.FunctionResponse.Name, responseError)
		default:
			return fmt.Errorf("%w: %s: %s", ErrToolExecutionFailed, part.FunctionResponse.Name, responseError)
		}
	}
	return nil
}

func responseErrorMessage(response map[string]any) string {
	if response == nil {
		return ""
	}
	for _, key := range []string{"error", "errorMessage"} {
		if value, ok := response[key]; ok && value != nil {
			message := strings.TrimSpace(fmt.Sprint(value))
			if message != "" && message != "<nil>" {
				return message
			}
		}
	}
	if output, ok := response["output"].(map[string]any); ok {
		return responseErrorMessage(output)
	}
	return ""
}

func FailureCode(err error) string {
	if code := aihubruntime.SkillFailureCode(err); code != "" {
		return code
	}
	switch {
	case errors.Is(err, permissiongate.ErrToolDenied):
		return "TOOL_AUTHORIZATION_DENIED"
	case errors.Is(err, ErrSkillContextToolFailed):
		return "SKILL_CONTEXT_TOOL_FAILED"
	case errors.Is(err, ErrToolExecutionFailed):
		return "TOOL_EXECUTION_FAILED"
	default:
		return "AGENT_RUN_FAILED"
	}
}

type Executor struct {
	Model             model.LLM
	SessionService    session.Service
	ToolRegistry      *toolruntime.Registry
	Tools             []tool.Tool
	Toolsets          []tool.Toolset
	AutoCreateSession bool
}

type RunRequest struct {
	Plan         *runtimeplan.RuntimePlan
	AppName      string
	UserID       string
	SessionID    string
	Message      string
	InvocationID string
	Streaming    bool
	StateDelta   map[string]any
}

func (e *Executor) Run(ctx context.Context, req RunRequest) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		r, err := e.runner(req)
		if err != nil {
			yield(nil, err)
			return
		}
		message := strings.TrimSpace(req.Message)
		if message == "" {
			yield(nil, fmt.Errorf("message is required"))
			return
		}
		runCfg := agent.RunConfig{}
		if req.Streaming {
			runCfg.StreamingMode = agent.StreamingModeSSE
		}
		options := []runner.RunOption{}
		if req.InvocationID != "" {
			options = append(options, runner.WithInvocationID(req.InvocationID))
		}
		if len(req.StateDelta) > 0 {
			options = append(options, runner.WithStateDelta(req.StateDelta))
		}
		for event, err := range r.Run(ctx, firstNonEmpty(req.UserID, "user"), firstNonEmpty(req.SessionID, req.Plan.SessionID), genai.NewContentFromText(message, genai.RoleUser), runCfg, options...) {
			if !yield(event, err) {
				return
			}
		}
	}
}

func (e *Executor) runner(req RunRequest) (*runner.Runner, error) {
	if req.Plan == nil {
		return nil, fmt.Errorf("runtime plan is required")
	}
	if e.Model == nil {
		return nil, fmt.Errorf("model adapter is required")
	}
	if e.SessionService == nil {
		return nil, fmt.Errorf("session service is required")
	}
	root, err := (&agentassembler.Assembler{
		Model: e.Model, Tools: e.Tools, Toolsets: e.Toolsets, ToolRegistry: e.ToolRegistry,
	}).Assemble(req.Plan)
	if err != nil {
		return nil, err
	}
	return runner.New(runner.Config{
		AppName: firstNonEmpty(req.AppName, req.Plan.Agent.ID),
		Agent:   root, SessionService: e.SessionService,
		AutoCreateSession: e.AutoCreateSession,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
