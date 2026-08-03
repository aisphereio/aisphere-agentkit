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

// Package subagenttaskrunnertool exposes configured sub-agents as task runners.
//
// It is intentionally built on top of the existing AgentTool execution path:
// sub_agents remain the single source of truth, but invocation.mode=task means
// "run this child as a background task and return to the parent" rather than
// "handoff the conversation with transfer_to_agent".
package subagenttaskrunnertool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"
	"google.golang.org/adk/tool/functiontool"
)

// WorkerConfig describes one task-mode sub-agent binding.
type WorkerConfig struct {
	ID                 string
	DisplayName        string
	Role               string
	Agent              agent.Agent
	ContextMode        string
	SessionKey         string
	ParallelSafe       bool
	MaxOutputChars     int
	DefaultConcurrency int
	SkipSummarization  bool
}

// NewToolset builds a runtime task runner for task-mode sub-agents.
func NewToolset(workers []WorkerConfig) (tool.Toolset, error) {
	ts := &Toolset{workers: map[string]*workerBinding{}}
	for _, cfg := range workers {
		if cfg.Agent == nil {
			return nil, fmt.Errorf("subagent task runner worker %q has nil agent", cfg.ID)
		}
		id := strings.TrimSpace(cfg.ID)
		if id == "" {
			id = cfg.Agent.Name()
		}
		if _, exists := ts.workers[id]; exists {
			return nil, fmt.Errorf("duplicate task-mode sub_agent id %q", id)
		}
		if cfg.DefaultConcurrency <= 0 {
			cfg.DefaultConcurrency = 1
		}
		at := agenttool.New(cfg.Agent, &agenttool.Config{
			SkipSummarization: cfg.SkipSummarization,
			ContextMode:       cfg.ContextMode,
			SessionKey:        cfg.SessionKey,
			ParallelSafe:      cfg.ParallelSafe || cfg.DefaultConcurrency > 1,
			MaxOutputChars:    cfg.MaxOutputChars,
		})
		ts.workers[id] = &workerBinding{cfg: cfg, tool: at}
	}

	ts.tools = make([]tool.Tool, 0, 3)
	if t, err := functiontool.New(functiontool.Config{
		Name:        "subagent_list_tasks",
		Description: "List sub-agents that can be run as background tasks from this controller. This is compact metadata only.",
	}, ts.List); err != nil {
		return nil, err
	} else {
		ts.tools = append(ts.tools, t)
	}
	if t, err := functiontool.New(functiontool.Config{
		Name:        "subagent_run_task",
		Description: "Run one configured task-mode sub-agent and return its compact result to the controller. The child does not take over the conversation.",
	}, ts.RunTask); err != nil {
		return nil, err
	} else {
		ts.tools = append(ts.tools, t)
	}
	if t, err := functiontool.New(functiontool.Config{
		Name:        "subagent_run_tasks",
		Description: "Run multiple tasks with one configured task-mode sub-agent, either serially or in parallel with max_concurrency. Returns compact per-task results and control returns to the controller.",
	}, ts.RunTasks); err != nil {
		return nil, err
	} else {
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

// Toolset exposes task runner tools.
type Toolset struct {
	workers map[string]*workerBinding
	tools   []tool.Tool
}

type workerBinding struct {
	cfg  WorkerConfig
	tool tool.Tool
}

func (t *Toolset) Name() string                                         { return "SubAgentTaskRunnerToolset" }
func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

// ListArgs is currently empty but kept as a struct for functiontool schema stability.
type ListArgs struct{}

type RunTaskArgs struct {
	AgentID         string         `json:"agent_id" jsonschema:"Task-mode sub_agent id from the controller's sub_agents list."`
	Task            map[string]any `json:"task,omitempty" jsonschema:"Structured task payload for the child agent, e.g. chapter_no/book_id/run_id."`
	Shared          map[string]any `json:"shared,omitempty" jsonschema:"Shared payload merged into the task, e.g. book_id/book_name/run_id/skill_id."`
	Request         string         `json:"request,omitempty" jsonschema:"Optional explicit request text. If empty, a JSON task envelope is sent."`
	ParentSessionID string         `json:"parent_session_id,omitempty" jsonschema:"Defaults to the controller's current session id. Worker can use this for workspace commit_to_parent."`
	RunID           string         `json:"run_id,omitempty" jsonschema:"Optional run id copied into the task envelope when set."`
	TaskID          string         `json:"task_id,omitempty" jsonschema:"Optional task id for tracking."`
}

type RunTasksArgs struct {
	AgentID         string           `json:"agent_id" jsonschema:"Task-mode sub_agent id from the controller's sub_agents list."`
	Tasks           []map[string]any `json:"tasks" jsonschema:"List of structured tasks to run."`
	Shared          map[string]any   `json:"shared,omitempty" jsonschema:"Shared payload merged into every task."`
	Mode            string           `json:"mode,omitempty" jsonschema:"serial or parallel. Defaults to the worker binding policy."`
	MaxConcurrency  int              `json:"max_concurrency,omitempty" jsonschema:"Parallelism cap. Defaults to sub_agent invocation.max_concurrency or 1."`
	ParentSessionID string           `json:"parent_session_id,omitempty" jsonschema:"Defaults to controller session id."`
	RunID           string           `json:"run_id,omitempty"`
}

type TaskResult struct {
	Index   int            `json:"index"`
	TaskID  string         `json:"task_id,omitempty"`
	OK      bool           `json:"ok"`
	Result  map[string]any `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
	Summary string         `json:"summary,omitempty"`
}

func (t *Toolset) List(ctx tool.Context, args ListArgs) (map[string]any, error) {
	ids := make([]string, 0, len(t.workers))
	for id := range t.workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		b := t.workers[id]
		policyDefault, policyMax := platformConcurrencyPolicy(ctx, b)
		items = append(items, map[string]any{
			"agent_id":                     id,
			"agent_name":                   b.cfg.Agent.Name(),
			"display_name":                 defaultString(b.cfg.DisplayName, b.cfg.Agent.Name()),
			"role":                         b.cfg.Role,
			"context_mode":                 defaultString(b.cfg.ContextMode, "inherit"),
			"parallel_safe":                b.cfg.ParallelSafe,
			"default_concurrency":          b.cfg.DefaultConcurrency,
			"platform_default_concurrency": policyDefault,
			"platform_max_concurrency":     policyMax,
			"max_output_chars":             b.cfg.MaxOutputChars,
		})
	}
	return map[string]any{"ok": true, "workers": items}, nil
}

func (t *Toolset) RunTask(ctx tool.Context, args RunTaskArgs) (map[string]any, error) {
	b, err := t.binding(args.AgentID)
	if err != nil {
		return nil, err
	}
	result, err := t.runOne(ctx, b, 0, args.TaskID, args.Shared, args.Task, args.Request, args.ParentSessionID, args.RunID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":       result.OK,
		"agent_id": strings.TrimSpace(args.AgentID),
		"result":   result.Result,
		"error":    result.Error,
	}, nil
}

func (t *Toolset) RunTasks(ctx tool.Context, args RunTasksArgs) (map[string]any, error) {
	b, err := t.binding(args.AgentID)
	if err != nil {
		return nil, err
	}
	if len(args.Tasks) == 0 {
		return nil, fmt.Errorf("tasks is empty")
	}
	decision := t.resolveConcurrency(ctx, b, args)
	maxConc := decision.Effective

	t.recordBatchPlan(ctx, b, args, decision)

	results := make([]TaskResult, len(args.Tasks))
	if maxConc == 1 {
		for i, task := range args.Tasks {
			results[i] = t.mustRunOne(ctx, b, i, taskIDFrom(task, i), args.Shared, task, "", args.ParentSessionID, args.RunID)
		}
	} else {
		sem := make(chan struct{}, maxConc)
		var wg sync.WaitGroup
		for i, task := range args.Tasks {
			i, task := i, task
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i] = t.mustRunOne(ctx, b, i, taskIDFrom(task, i), args.Shared, task, "", args.ParentSessionID, args.RunID)
			}()
		}
		wg.Wait()
	}

	completed := 0
	failed := 0
	refs := []any{}
	for _, r := range results {
		if r.OK {
			completed++
		} else {
			failed++
		}
		if r.Result != nil {
			collectRefs(r.Result, &refs)
		}
	}
	t.recordBatchCompleted(ctx, b, args, decision, completed, failed, refs, results)
	return map[string]any{
		"ok":                       failed == 0,
		"agent_id":                 strings.TrimSpace(args.AgentID),
		"mode":                     decision.Mode,
		"max_concurrency":          decision.Effective,
		"requested_concurrency":    decision.Requested,
		"default_concurrency":      decision.Default,
		"platform_max_concurrency": decision.Max,
		"concurrency_source":       decision.Source,
		"concurrency_was_clamped":  decision.Clamped,
		"total":                    len(args.Tasks),
		"completed":                completed,
		"failed":                   failed,
		"results":                  results,
		"artifact_refs":            refs,
	}, nil
}

type concurrencyDecision struct {
	Mode      string
	Requested int
	Effective int
	Default   int
	Max       int
	Source    string
	Clamped   bool
}

func (t *Toolset) resolveConcurrency(ctx tool.Context, b *workerBinding, args RunTasksArgs) concurrencyDecision {
	policyDefault, policyMax := platformConcurrencyPolicy(ctx, b)
	requested := args.MaxConcurrency
	effective := policyDefault
	source := "platform_default"
	if requested > 0 {
		effective = requested
		source = "agent_requested"
	}
	clamped := false
	if policyMax > 0 && effective > policyMax {
		effective = policyMax
		clamped = true
	}
	if effective <= 0 {
		effective = 1
	}
	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	if mode == "" {
		if effective > 1 || b.cfg.ParallelSafe {
			mode = "parallel"
		} else {
			mode = "serial"
		}
	}
	if mode != "parallel" {
		effective = 1
	}
	return concurrencyDecision{
		Mode:      mode,
		Requested: requested,
		Effective: effective,
		Default:   policyDefault,
		Max:       policyMax,
		Source:    source,
		Clamped:   clamped,
	}
}

func platformConcurrencyPolicy(ctx context.Context, b *workerBinding) (int, int) {
	workerDefault := 1
	if b != nil && b.cfg.DefaultConcurrency > 0 {
		workerDefault = b.cfg.DefaultConcurrency
	}
	cfg := runtimeconfig.FromContext(ctx)
	policyDefault := workerDefault
	policyMax := workerDefault
	if cfg != nil {
		if cfg.Runtime.SubAgentTasks.DefaultConcurrency > 0 {
			policyDefault = cfg.Runtime.SubAgentTasks.DefaultConcurrency
		}
		if cfg.Runtime.SubAgentTasks.MaxConcurrency > 0 {
			policyMax = cfg.Runtime.SubAgentTasks.MaxConcurrency
		}
	}
	if policyMax <= 0 {
		policyMax = workerDefault
	}
	if policyDefault <= 0 {
		policyDefault = 1
	}
	if policyDefault > policyMax {
		policyDefault = policyMax
	}
	return policyDefault, policyMax
}

func (t *Toolset) binding(agentID string) (*workerBinding, error) {
	id := strings.TrimSpace(agentID)
	if id == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	b, ok := t.workers[id]
	if !ok {
		ids := make([]string, 0, len(t.workers))
		for k := range t.workers {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("task-mode sub_agent %q not found; available: %v", id, ids)
	}
	return b, nil
}

func (t *Toolset) mustRunOne(ctx tool.Context, b *workerBinding, index int, taskID string, shared, task map[string]any, request, parentSessionID, runID string) TaskResult {
	res, err := t.runOne(ctx, b, index, taskID, shared, task, request, parentSessionID, runID)
	if err != nil {
		return TaskResult{Index: index, TaskID: taskID, OK: false, Error: err.Error()}
	}
	return res
}

func (t *Toolset) runOne(ctx tool.Context, b *workerBinding, index int, taskID string, shared, task map[string]any, request, parentSessionID, runID string) (TaskResult, error) {
	if parentSessionID == "" {
		parentSessionID = ctx.SessionID()
	}
	payload := mergeMaps(shared, task)
	payload["parent_session_id"] = parentSessionID
	payload["parent_app_name"] = ctx.AppName()
	payload["controller_agent"] = ctx.AgentName()
	payload["child_agent"] = b.cfg.Agent.Name()
	if runID != "" {
		payload["run_id"] = runID
	}
	if taskID != "" {
		payload["task_id"] = taskID
	}
	if _, ok := payload["task_index"]; !ok {
		payload["task_index"] = index
	}

	input := map[string]any{}
	if strings.TrimSpace(request) != "" {
		input["request"] = request + "\n\nTask envelope:\n" + mustJSON(payload)
	} else {
		input["request"] = "Execute this sub-agent task. Use the task envelope exactly; save large outputs to session workspace and return only compact refs.\n\n" + mustJSON(payload)
	}
	input["__adk_subagent_trace"] = t.subAgentTraceMeta(ctx, b, index, taskID, payload)

	t.recordTaskStarted(ctx, b, index, taskID, payload)

	runner, ok := b.tool.(interface {
		Run(tool.Context, any) (map[string]any, error)
	})
	if !ok {
		return TaskResult{}, fmt.Errorf("internal error: worker tool %s is not runnable", b.cfg.Agent.Name())
	}
	out, err := runner.Run(ctx, input)
	if err != nil {
		t.recordTaskFailed(ctx, b, index, taskID, payload, err)
		return TaskResult{}, err
	}
	refs := []any{}
	collectRefs(out, &refs)
	result := TaskResult{Index: index, TaskID: taskID, OK: true, Result: out, Summary: compactSummary(out)}
	t.recordTaskCompleted(ctx, b, index, taskID, payload, refs, result)
	return result, nil
}

func displayAgentName(b *workerBinding) string {
	if b == nil {
		return ""
	}
	return defaultString(b.cfg.DisplayName, b.cfg.Agent.Name())
}

func bindingID(b *workerBinding) string {
	if b == nil {
		return ""
	}
	id := strings.TrimSpace(b.cfg.ID)
	if id == "" && b.cfg.Agent != nil {
		id = b.cfg.Agent.Name()
	}
	return id
}

func (t *Toolset) subAgentTraceMeta(ctx tool.Context, b *workerBinding, index int, taskID string, payload map[string]any) map[string]any {
	return map[string]any{
		"agent_id":             bindingID(b),
		"agent_name":           b.cfg.Agent.Name(),
		"display_name":         displayAgentName(b),
		"role":                 b.cfg.Role,
		"index":                index,
		"task_id":              taskID,
		"chapter_no":           payload["chapter_no"],
		"run_id":               payload["run_id"],
		"payload":              compactTaskPayload(payload),
		"parent_invocation_id": ctx.InvocationID(),
		"parent_session_id":    defaultString(stringFromAny(payload["parent_session_id"]), ctx.SessionID()),
		"parent_app_name":      defaultString(stringFromAny(payload["parent_app_name"]), ctx.AppName()),
		"parent_agent_name":    ctx.AgentName(),
	}
}

func (t *Toolset) recordBatchPlan(ctx tool.Context, b *workerBinding, args RunTasksArgs, decision concurrencyDecision) {
	runtimetrace.Record(ctx, runtimetrace.EventSubAgentTaskPlan, map[string]any{
		"agent_id":                 strings.TrimSpace(args.AgentID),
		"agent_name":               b.cfg.Agent.Name(),
		"display_name":             displayAgentName(b),
		"role":                     b.cfg.Role,
		"mode":                     decision.Mode,
		"max_concurrency":          decision.Effective,
		"requested_concurrency":    decision.Requested,
		"default_concurrency":      decision.Default,
		"platform_max_concurrency": decision.Max,
		"concurrency_source":       decision.Source,
		"concurrency_was_clamped":  decision.Clamped,
		"task_count":               len(args.Tasks),
		"run_id":                   args.RunID,
		"parent_session_id":        defaultString(args.ParentSessionID, ctx.SessionID()),
		"task_ids":                 taskIDsFrom(args.Tasks),
		"tasks":                    compactPlannedTasks(args.Tasks),
		"ui": map[string]any{
			"component":  "subagent_task_group",
			"status":     "planned",
			"expandable": true,
			"title":      fmt.Sprintf("%s 脳 %d", displayAgentName(b), len(args.Tasks)),
		},
	})
}

func (t *Toolset) recordBatchCompleted(ctx tool.Context, b *workerBinding, args RunTasksArgs, decision concurrencyDecision, completed, failed int, refs []any, results []TaskResult) {
	runtimetrace.Record(ctx, runtimetrace.EventSubAgentTaskBatchCompleted, map[string]any{
		"agent_id":                 strings.TrimSpace(args.AgentID),
		"agent_name":               b.cfg.Agent.Name(),
		"display_name":             displayAgentName(b),
		"role":                     b.cfg.Role,
		"mode":                     decision.Mode,
		"max_concurrency":          decision.Effective,
		"requested_concurrency":    decision.Requested,
		"default_concurrency":      decision.Default,
		"platform_max_concurrency": decision.Max,
		"concurrency_source":       decision.Source,
		"concurrency_was_clamped":  decision.Clamped,
		"total":                    len(results),
		"completed":                completed,
		"failed":                   failed,
		"ok":                       failed == 0,
		"artifact_refs":            refs,
		"results":                  compactTaskResults(results),
		"ui": map[string]any{
			"component":  "subagent_task_group",
			"status":     statusFromFailed(failed),
			"expandable": true,
			"title":      fmt.Sprintf("%s completed %d/%d", displayAgentName(b), completed, len(results)),
		},
	})
}

func (t *Toolset) recordTaskStarted(ctx tool.Context, b *workerBinding, index int, taskID string, payload map[string]any) {
	runtimetrace.Record(ctx, runtimetrace.EventSubAgentTaskStarted, map[string]any{
		"agent_id":          bindingID(b),
		"agent_name":        b.cfg.Agent.Name(),
		"display_name":      displayAgentName(b),
		"role":              b.cfg.Role,
		"index":             index,
		"task_id":           taskID,
		"chapter_no":        payload["chapter_no"],
		"run_id":            payload["run_id"],
		"parent_session_id": payload["parent_session_id"],
		"child_agent":       payload["child_agent"],
		"payload":           compactTaskPayload(payload),
		"ui": map[string]any{
			"component":  "subagent_task_card",
			"status":     "running",
			"expandable": true,
			"title":      taskTitle(displayAgentName(b), taskID, payload),
		},
	})
}

func (t *Toolset) recordTaskCompleted(ctx tool.Context, b *workerBinding, index int, taskID string, payload map[string]any, refs []any, result TaskResult) {
	runtimetrace.Record(ctx, runtimetrace.EventSubAgentTaskCompleted, map[string]any{
		"agent_id":          bindingID(b),
		"agent_name":        b.cfg.Agent.Name(),
		"display_name":      displayAgentName(b),
		"role":              b.cfg.Role,
		"index":             index,
		"task_id":           taskID,
		"chapter_no":        payload["chapter_no"],
		"run_id":            payload["run_id"],
		"parent_session_id": payload["parent_session_id"],
		"artifact_refs":     refs,
		"summary":           result.Summary,
		"result":            compactTaskResult(result),
		"ui": map[string]any{
			"component":  "subagent_task_card",
			"status":     "completed",
			"expandable": true,
			"title":      taskTitle(displayAgentName(b), taskID, payload),
		},
	})
}

func (t *Toolset) recordTaskFailed(ctx tool.Context, b *workerBinding, index int, taskID string, payload map[string]any, err error) {
	runtimetrace.Record(ctx, runtimetrace.EventSubAgentTaskFailed, map[string]any{
		"agent_id":          bindingID(b),
		"agent_name":        b.cfg.Agent.Name(),
		"display_name":      displayAgentName(b),
		"role":              b.cfg.Role,
		"index":             index,
		"task_id":           taskID,
		"chapter_no":        payload["chapter_no"],
		"run_id":            payload["run_id"],
		"parent_session_id": payload["parent_session_id"],
		"error":             err.Error(),
		"ui": map[string]any{
			"component":  "subagent_task_card",
			"status":     "failed",
			"expandable": true,
			"title":      taskTitle(displayAgentName(b), taskID, payload),
		},
	})
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func taskIDFrom(task map[string]any, idx int) string {
	for _, k := range []string{"task_id", "id", "chapter_id"} {
		if v, ok := task[k]; ok && strings.TrimSpace(fmt.Sprint(v)) != "" {
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	if v, ok := task["chapter_no"]; ok {
		return fmt.Sprintf("chapter_%04d", intFromAny(v, idx+1))
	}
	return fmt.Sprintf("task_%04d", idx+1)
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func compactSummary(m map[string]any) string {
	if m == nil {
		return ""
	}
	if s, ok := m["summary"].(string); ok {
		return s
	}
	if s, ok := m["result"].(string); ok {
		r := []rune(s)
		if len(r) > 240 {
			return string(r[:240]) + "..."
		}
		return s
	}
	return ""
}

func collectRefs(v any, refs *[]any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if strings.Contains(strings.ToLower(k), "ref") {
				*refs = append(*refs, v)
			}
			collectRefs(v, refs)
		}
	case []any:
		for _, item := range x {
			collectRefs(item, refs)
		}
	}
}

func compactPlannedTasks(tasks []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for i, task := range tasks {
		item := compactTaskPayload(task)
		item["index"] = i
		item["task_id"] = taskIDFrom(task, i)
		if _, ok := item["task_index"]; !ok {
			item["task_index"] = i
		}
		out = append(out, item)
	}
	return out
}

func taskIDsFrom(tasks []map[string]any) []string {
	out := make([]string, 0, len(tasks))
	for i, task := range tasks {
		out = append(out, taskIDFrom(task, i))
	}
	return out
}

func compactTaskPayload(payload map[string]any) map[string]any {
	keys := []string{"run_id", "task_id", "task_index", "book_id", "book_name", "chapter_no", "skill_id", "parent_session_id", "parent_app_name", "controller_agent", "child_agent"}
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := payload[k]; ok {
			out[k] = v
		}
	}
	return out
}

func compactTaskResults(results []TaskResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, compactTaskResult(r))
	}
	return out
}

func compactTaskResult(r TaskResult) map[string]any {
	refs := []any{}
	collectRefs(r.Result, &refs)
	return map[string]any{
		"index":         r.Index,
		"task_id":       r.TaskID,
		"ok":            r.OK,
		"error":         r.Error,
		"summary":       r.Summary,
		"artifact_refs": refs,
	}
}

func statusFromFailed(failed int) string {
	if failed > 0 {
		return "failed"
	}
	return "completed"
}

func taskTitle(agentName, taskID string, payload map[string]any) string {
	if ch, ok := payload["chapter_no"]; ok {
		return fmt.Sprintf("%s 路 chapter %v", agentName, ch)
	}
	if taskID != "" {
		return fmt.Sprintf("%s 路 %s", agentName, taskID)
	}
	return agentName
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func intFromAny(v any, fallback int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return fallback
}

func defaultString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}
