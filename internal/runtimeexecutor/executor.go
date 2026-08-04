// Package runtimeexecutor runs Hub-authorized RuntimePlans through ADK-Go.
package runtimeexecutor

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/agentassembler"
	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/toolruntime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

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
