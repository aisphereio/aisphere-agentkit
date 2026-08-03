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

// Package delegationruntool provides a generic background delegation runtime.
//
// This is intentionally domain-neutral. A manager agent should create a plan and
// delegate bounded, isolated tasks to professional worker agents. The tool does
// not know what a novel, chapter, dialogue, Kubernetes pod, or skill is. It only
// creates independent ADK sessions with bounded concurrency and keeps progress
// out of the manager session.
package delegationruntool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const (
	statusQueued    = "queued"
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
)

// Config configures the toolset. It calls the ADK REST API so delegated tasks
// use real isolated sessions rather than polluting the manager session.
type Config struct {
	RuntimeBaseURL string
	Headers        map[string]string
	MaxConcurrency int
}

func ConfigFromMap(args map[string]any) Config {
	cfg := Config{
		RuntimeBaseURL: os.Getenv("ADK_RUNTIME_BASE_URL"),
		Headers:        map[string]string{},
		MaxConcurrency: intFromAny(args["max_concurrency"], 8),
	}
	if cfg.RuntimeBaseURL == "" {
		cfg.RuntimeBaseURL = "http://127.0.0.1:8080"
	}
	if v, ok := args["runtime_base_url"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.RuntimeBaseURL = os.ExpandEnv(strings.TrimSpace(v))
	}
	if raw, ok := args["headers"].(map[string]any); ok {
		for k, v := range raw {
			cfg.Headers[k] = os.ExpandEnv(fmt.Sprint(v))
		}
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 8
	}
	if cfg.MaxConcurrency > 128 {
		cfg.MaxConcurrency = 128
	}
	return cfg
}

// NewToolset creates the generic delegation toolset.
func NewToolset(cfg Config) (tool.Toolset, error) {
	ts := &Toolset{cfg: cfg}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:          "delegation_run_start",
				Description:   "Start a generic background delegation run. Use this when a task should be split into many isolated worker-agent sessions. The manager supplies the worker agent app and task prompts; this tool only handles concurrency, sessions, status, and optional final delegation.",
				IsLongRunning: true,
			}, ts.Start)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "delegation_run_get",
				Description: "Get status/progress for a background delegation run started by delegation_run_start.",
			}, ts.Get)
		},
	}
	for _, build := range builders {
		t, err := build()
		if err != nil {
			return nil, err
		}
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

type Toolset struct {
	cfg   Config
	tools []tool.Tool
}

func (t *Toolset) Name() string                                         { return "DelegationRunToolset" }
func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

// TaskSpec is one delegated unit. It is intentionally generic.
type TaskSpec struct {
	TaskID      string            `json:"task_id,omitempty" jsonschema:"Stable task id. If empty, generated from index."`
	AgentApp    string            `json:"agent_app,omitempty" jsonschema:"ADK app/agent used for this task. Defaults to default_agent_app."`
	Prompt      string            `json:"prompt" jsonschema:"Task prompt passed to the worker agent. Keep it bounded; pass ids/object keys, not huge content."`
	State       map[string]any    `json:"state,omitempty" jsonschema:"Optional state copied to the worker session."`
	Metadata    map[string]string `json:"metadata,omitempty" jsonschema:"Small metadata for status display."`
	SessionID   string            `json:"session_id,omitempty" jsonschema:"Optional explicit worker session id."`
	Description string            `json:"description,omitempty" jsonschema:"Human-readable task description."`
}

// RangeSpec expands a numeric range into tasks without making the model list
// hundreds of explicit task objects.
type RangeSpec struct {
	From           int               `json:"from" jsonschema:"Start number, inclusive."`
	To             int               `json:"to" jsonschema:"End number, inclusive."`
	Step           int               `json:"step,omitempty" jsonschema:"Step. Defaults to 1."`
	AgentApp       string            `json:"agent_app,omitempty" jsonschema:"Worker app for all generated tasks. Defaults to default_agent_app."`
	TaskIDTemplate string            `json:"task_id_template,omitempty" jsonschema:"Template using {{item}}, {{item04}}, {{run_id}}. Defaults to task_{{item04}}."`
	PromptTemplate string            `json:"prompt_template" jsonschema:"Template using {{item}}, {{item04}}, {{run_id}}, {{objective}}."`
	Description    string            `json:"description,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	State          map[string]any    `json:"state,omitempty"`
}

type FinalSpec struct {
	AgentApp  string         `json:"agent_app,omitempty" jsonschema:"Optional final/reducer agent app."`
	Prompt    string         `json:"prompt,omitempty" jsonschema:"Prompt for the final/reducer agent. It should read saved small artifacts, not raw huge content."`
	State     map[string]any `json:"state,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
}

type StartArgs struct {
	RunID           string            `json:"run_id,omitempty" jsonschema:"Optional stable run id. Defaults to generated."`
	RunType         string            `json:"run_type,omitempty" jsonschema:"Domain label, for example novel_dialogue_skill, ops_log_review, test_generation."`
	Objective       string            `json:"objective" jsonschema:"Human-readable objective for the manager and workers."`
	DefaultAgentApp string            `json:"default_agent_app,omitempty" jsonschema:"Default ADK worker app/agent for tasks that do not set agent_app."`
	Tasks           []TaskSpec        `json:"tasks,omitempty" jsonschema:"Explicit delegated tasks. Use this for heterogeneous workers or non-numeric items."`
	Range           *RangeSpec        `json:"range,omitempty" jsonschema:"Generate tasks from a numeric range without listing every task."`
	Final           *FinalSpec        `json:"final,omitempty" jsonschema:"Optional final/reducer delegated task after all worker tasks succeed."`
	Concurrency     int               `json:"concurrency,omitempty" jsonschema:"Parallel worker count. Capped by tool config."`
	UserID          string            `json:"user_id,omitempty" jsonschema:"Defaults to caller user id."`
	ProjectID       string            `json:"project_id,omitempty" jsonschema:"Project/workspace id to copy into worker sessions."`
	SharedState     map[string]any    `json:"shared_state,omitempty" jsonschema:"Small state copied to every worker session."`
	Metadata        map[string]string `json:"metadata,omitempty" jsonschema:"Small run metadata for status."`
}

type StartResult struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	Total       int    `json:"total"`
	Concurrency int    `json:"concurrency"`
	Message     string `json:"message"`
}

type GetArgs struct {
	RunID string `json:"run_id"`
}

type GetResult struct {
	Run *RunState `json:"run"`
}

type RunState struct {
	RunID        string            `json:"run_id"`
	RunType      string            `json:"run_type,omitempty"`
	Objective    string            `json:"objective"`
	Status       string            `json:"status"`
	Total        int               `json:"total"`
	Completed    int               `json:"completed"`
	Failed       int               `json:"failed"`
	Running      int               `json:"running"`
	Concurrency  int               `json:"concurrency"`
	Tasks        []TaskState       `json:"tasks,omitempty"`
	FinalStatus  string            `json:"final_status,omitempty"`
	FinalSession string            `json:"final_session_id,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

type TaskState struct {
	TaskID       string            `json:"task_id"`
	AgentApp     string            `json:"agent_app"`
	Status       string            `json:"status"`
	SessionID    string            `json:"session_id,omitempty"`
	Description  string            `json:"description,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	StartedAt    string            `json:"started_at,omitempty"`
	CompletedAt  string            `json:"completed_at,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

var (
	runsMu sync.Mutex
	runs   = map[string]*RunState{}
)

func (t *Toolset) Start(ctx tool.Context, args StartArgs) (StartResult, error) {
	if strings.TrimSpace(args.Objective) == "" {
		return StartResult{}, fmt.Errorf("objective is required")
	}
	if args.UserID == "" {
		args.UserID = ctx.UserID()
	}
	if args.ProjectID == "" {
		if v, err := ctx.State().Get("project_id"); err == nil {
			args.ProjectID = fmt.Sprint(v)
		}
	}
	concurrency := args.Concurrency
	if concurrency <= 0 {
		concurrency = t.cfg.MaxConcurrency
	}
	if concurrency > t.cfg.MaxConcurrency {
		concurrency = t.cfg.MaxConcurrency
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	runID := strings.TrimSpace(args.RunID)
	if runID == "" {
		runID = "delegate_" + time.Now().UTC().Format("20060102_150405") + "_" + uuid.NewString()[:8]
	}
	tasks, err := expandTasks(runID, args)
	if err != nil {
		return StartResult{}, err
	}
	if len(tasks) == 0 {
		return StartResult{}, fmt.Errorf("no delegated tasks; provide tasks or range")
	}

	state := &RunState{
		RunID: runID, RunType: args.RunType, Objective: args.Objective, Status: statusQueued,
		Total: len(tasks), Concurrency: concurrency, Metadata: args.Metadata, CreatedAt: now(), UpdatedAt: now(),
	}
	for _, task := range tasks {
		state.Tasks = append(state.Tasks, TaskState{TaskID: task.TaskID, AgentApp: task.AgentApp, Status: statusQueued, SessionID: task.SessionID, Description: task.Description, Metadata: task.Metadata})
	}
	runsMu.Lock()
	if _, exists := runs[runID]; exists {
		runsMu.Unlock()
		return StartResult{}, fmt.Errorf("delegation run %q already exists", runID)
	}
	runs[runID] = state
	runsMu.Unlock()

	go t.run(context.Background(), runID, args, tasks, concurrency)

	return StartResult{RunID: runID, Status: statusQueued, Total: len(tasks), Concurrency: concurrency, Message: "delegation run accepted; use delegation_run_get to check progress"}, nil
}

func (t *Toolset) Get(ctx tool.Context, args GetArgs) (GetResult, error) {
	runsMu.Lock()
	defer runsMu.Unlock()
	st, ok := runs[args.RunID]
	if !ok {
		return GetResult{}, fmt.Errorf("delegation run %q not found", args.RunID)
	}
	cp := *st
	cp.Tasks = append([]TaskState(nil), st.Tasks...)
	return GetResult{Run: &cp}, nil
}

func (t *Toolset) run(ctx context.Context, runID string, args StartArgs, tasks []TaskSpec, concurrency int) {
	t.update(runID, func(st *RunState) { st.Status = statusRunning; st.UpdatedAt = now() })
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, task := range tasks {
		task := task
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			t.runOne(ctx, runID, args, task)
		}()
	}
	wg.Wait()
	var failed int
	t.update(runID, func(st *RunState) { failed = st.Failed })
	if failed > 0 {
		t.update(runID, func(st *RunState) {
			st.Status = statusFailed
			st.ErrorMessage = fmt.Sprintf("%d/%d delegated tasks failed", st.Failed, st.Total)
			st.UpdatedAt = now()
		})
		return
	}
	if args.Final != nil && strings.TrimSpace(args.Final.AgentApp) != "" && strings.TrimSpace(args.Final.Prompt) != "" {
		t.update(runID, func(st *RunState) { st.FinalStatus = statusRunning; st.UpdatedAt = now() })
		if err := t.runFinal(ctx, runID, args); err != nil {
			t.update(runID, func(st *RunState) {
				st.Status = statusFailed
				st.FinalStatus = statusFailed
				st.ErrorMessage = err.Error()
				st.UpdatedAt = now()
			})
			return
		}
		t.update(runID, func(st *RunState) { st.FinalStatus = statusCompleted; st.UpdatedAt = now() })
	}
	t.update(runID, func(st *RunState) { st.Status = statusCompleted; st.UpdatedAt = now() })
}

func (t *Toolset) runOne(ctx context.Context, runID string, args StartArgs, task TaskSpec) {
	sessionID := task.SessionID
	if sessionID == "" {
		sessionID = safeID(runID + "_" + task.TaskID)
	}
	t.updateTask(runID, task.TaskID, func(ts *TaskState) { ts.Status = statusRunning; ts.SessionID = sessionID; ts.StartedAt = now() })
	state := mergeState(args.SharedState, task.State)
	state["delegation_run_id"] = runID
	state["delegation_task_id"] = task.TaskID
	state["delegation_objective"] = args.Objective
	err := t.createSessionAndRun(ctx, task.AgentApp, args.UserID, sessionID, args.ProjectID, state, task.Prompt)
	if err != nil {
		t.updateTask(runID, task.TaskID, func(ts *TaskState) { ts.Status = statusFailed; ts.CompletedAt = now(); ts.ErrorMessage = err.Error() })
		t.update(runID, func(st *RunState) { st.Running--; st.Failed++; st.UpdatedAt = now() })
		return
	}
	t.updateTask(runID, task.TaskID, func(ts *TaskState) { ts.Status = statusCompleted; ts.CompletedAt = now() })
	t.update(runID, func(st *RunState) { st.Running--; st.Completed++; st.UpdatedAt = now() })
}

func (t *Toolset) runFinal(ctx context.Context, runID string, args StartArgs) error {
	final := args.Final
	sessionID := final.SessionID
	if sessionID == "" {
		sessionID = safeID(runID + "_final")
	}
	t.update(runID, func(st *RunState) { st.FinalSession = sessionID })
	state := mergeState(args.SharedState, final.State)
	state["delegation_run_id"] = runID
	state["delegation_final"] = true
	state["delegation_objective"] = args.Objective
	return t.createSessionAndRun(ctx, final.AgentApp, args.UserID, sessionID, args.ProjectID, state, final.Prompt)
}

func (t *Toolset) createSessionAndRun(ctx context.Context, appName, userID, sessionID, projectID string, state map[string]any, prompt string) error {
	if appName == "" {
		return fmt.Errorf("agent app is required")
	}
	if state == nil {
		state = map[string]any{}
	}
	if projectID != "" {
		state["project_id"] = projectID
		state["projectId"] = projectID
	}
	createURL := fmt.Sprintf("%s/apps/%s/users/%s/sessions/%s", strings.TrimRight(t.cfg.RuntimeBaseURL, "/"), url.PathEscape(appName), url.PathEscape(userID), url.PathEscape(sessionID))
	if err := t.postJSON(ctx, createURL, map[string]any{"state": state}, nil); err != nil && !strings.Contains(err.Error(), "already") {
		return fmt.Errorf("create session %s/%s: %w", appName, sessionID, err)
	}
	runURL := strings.TrimRight(t.cfg.RuntimeBaseURL, "/") + "/run"
	req := map[string]any{
		"appName":    appName,
		"userId":     userID,
		"sessionId":  sessionID,
		"newMessage": map[string]any{"role": "user", "parts": []map[string]any{{"text": prompt}}},
		"streaming":  false,
		"projectId":  projectID,
		"project_id": projectID,
		"stateDelta": state,
	}
	var resp any
	return t.postJSON(ctx, runURL, req, &resp)
}

func (t *Toolset) postJSON(ctx context.Context, endpoint string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, string(b))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func expandTasks(runID string, args StartArgs) ([]TaskSpec, error) {
	var tasks []TaskSpec
	for i, task := range args.Tasks {
		if strings.TrimSpace(task.AgentApp) == "" {
			task.AgentApp = args.DefaultAgentApp
		}
		if strings.TrimSpace(task.AgentApp) == "" {
			return nil, fmt.Errorf("tasks[%d].agent_app or default_agent_app is required", i)
		}
		if strings.TrimSpace(task.Prompt) == "" {
			return nil, fmt.Errorf("tasks[%d].prompt is required", i)
		}
		if strings.TrimSpace(task.TaskID) == "" {
			task.TaskID = fmt.Sprintf("task_%04d", i+1)
		}
		tasks = append(tasks, task)
	}
	if args.Range != nil {
		r := args.Range
		if r.From == 0 || r.To == 0 {
			return nil, fmt.Errorf("range.from and range.to are required")
		}
		step := r.Step
		if step == 0 {
			step = 1
		}
		if (r.To-r.From)*step < 0 {
			return nil, fmt.Errorf("range step moves away from target")
		}
		agentApp := strings.TrimSpace(r.AgentApp)
		if agentApp == "" {
			agentApp = args.DefaultAgentApp
		}
		if agentApp == "" {
			return nil, fmt.Errorf("range.agent_app or default_agent_app is required")
		}
		if strings.TrimSpace(r.PromptTemplate) == "" {
			return nil, fmt.Errorf("range.prompt_template is required")
		}
		tmplID := r.TaskIDTemplate
		if tmplID == "" {
			tmplID = "task_{{item04}}"
		}
		idx := 0
		for item := r.From; ; item += step {
			idx++
			repl := replacementMap(runID, args.Objective, item)
			tasks = append(tasks, TaskSpec{
				TaskID:      applyTemplate(tmplID, repl),
				AgentApp:    agentApp,
				Prompt:      applyTemplate(r.PromptTemplate, repl),
				State:       cloneMap(r.State),
				Metadata:    cloneStringMap(r.Metadata),
				Description: applyTemplate(r.Description, repl),
			})
			if item == r.To || (step > 0 && item+step > r.To) || (step < 0 && item+step < r.To) {
				break
			}
			if idx > 10000 {
				return nil, fmt.Errorf("range expanded to too many tasks")
			}
		}
	}
	return tasks, nil
}

func replacementMap(runID, objective string, item int) map[string]string {
	return map[string]string{
		"{{run_id}}":    runID,
		"{{objective}}": objective,
		"{{item}}":      fmt.Sprintf("%d", item),
		"{{item04}}":    fmt.Sprintf("%04d", item),
	}
}

func applyTemplate(s string, repl map[string]string) string {
	for k, v := range repl {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

func mergeState(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (t *Toolset) update(runID string, fn func(*RunState)) {
	runsMu.Lock()
	defer runsMu.Unlock()
	if st := runs[runID]; st != nil {
		fn(st)
		running := 0
		for _, task := range st.Tasks {
			if task.Status == statusRunning {
				running++
			}
		}
		st.Running = running
		sort.Slice(st.Tasks, func(i, j int) bool { return st.Tasks[i].TaskID < st.Tasks[j].TaskID })
	}
}

func (t *Toolset) updateTask(runID, taskID string, fn func(*TaskState)) {
	runsMu.Lock()
	defer runsMu.Unlock()
	if st := runs[runID]; st != nil {
		for i := range st.Tasks {
			if st.Tasks[i].TaskID == taskID {
				fn(&st.Tasks[i])
				break
			}
		}
		running := 0
		for _, task := range st.Tasks {
			if task.Status == statusRunning {
				running++
			}
		}
		st.Running = running
		st.UpdatedAt = now()
	}
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func safeID(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

func intFromAny(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var n int
		if _, err := fmt.Sscanf(x, "%d", &n); err == nil {
			return n
		}
	}
	return def
}
