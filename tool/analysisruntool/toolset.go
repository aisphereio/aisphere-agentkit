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

// Package analysisruntool starts background fan-out/fan-in analysis runs.
//
// It is intentionally a runtime tool, not a prompt trick. The manager agent calls
// analysis_run_start once. This tool then creates independent ADK sessions for a
// worker agent, runs them with bounded concurrency, and keeps only run progress in
// the manager session. Chapter text stays inside each worker session and should
// be saved as artifacts / MCP assets by the worker.
package analysisruntool

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

// Config configures the toolset. It is intentionally self-contained and calls
// the ADK REST API so it can create real isolated sessions for worker apps.
type Config struct {
	RuntimeBaseURL   string
	Headers          map[string]string
	DefaultWorkerApp string
	DefaultFinalApp  string
	MaxConcurrency   int
}

func ConfigFromMap(args map[string]any) Config {
	cfg := Config{
		RuntimeBaseURL:   os.Getenv("ADK_RUNTIME_BASE_URL"),
		Headers:          map[string]string{},
		DefaultWorkerApp: os.Getenv("ADK_ANALYSIS_WORKER_APP"),
		DefaultFinalApp:  os.Getenv("ADK_ANALYSIS_FINAL_APP"),
		MaxConcurrency:   intFromAny(args["max_concurrency"], 8),
	}
	if cfg.RuntimeBaseURL == "" {
		cfg.RuntimeBaseURL = "http://127.0.0.1:8080"
	}
	if cfg.DefaultWorkerApp == "" {
		cfg.DefaultWorkerApp = "chapter_dialogue_worker_mcp"
	}
	if cfg.DefaultFinalApp == "" {
		cfg.DefaultFinalApp = "dialogue_skill_distiller_mcp"
	}
	if v, ok := args["runtime_base_url"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.RuntimeBaseURL = os.ExpandEnv(strings.TrimSpace(v))
	}
	if v, ok := args["default_worker_app"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.DefaultWorkerApp = strings.TrimSpace(v)
	}
	if v, ok := args["default_final_app"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.DefaultFinalApp = strings.TrimSpace(v)
	}
	if raw, ok := args["headers"].(map[string]any); ok {
		for k, v := range raw {
			cfg.Headers[k] = os.ExpandEnv(fmt.Sprint(v))
		}
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 8
	}
	if cfg.MaxConcurrency > 64 {
		cfg.MaxConcurrency = 64
	}
	return cfg
}

// NewToolset creates the analysis run toolset.
func NewToolset(cfg Config) (tool.Toolset, error) {
	ts := &Toolset{cfg: cfg}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:          "analysis_run_start",
				Description:   "Start a background parallel analysis run. Use this for long tasks such as analyzing 100+ chapters. It creates isolated worker sessions and returns run_id immediately; do not read many chapters into the current chat session.",
				IsLongRunning: true,
			}, ts.Start)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "analysis_run_get",
				Description: "Get status/progress for a background analysis run started by analysis_run_start.",
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

// Toolset groups analysis run tools.
type Toolset struct {
	cfg   Config
	tools []tool.Tool
}

func (t *Toolset) Name() string                                         { return "AnalysisRunToolset" }
func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

type StartArgs struct {
	BookID           string `json:"book_id" jsonschema:"Novel splitter book id."`
	BookName         string `json:"book_name,omitempty"`
	AnalysisType     string `json:"analysis_type,omitempty" jsonschema:"dialogue, plot, character, style. Defaults to dialogue."`
	StartChapter     int    `json:"start_chapter,omitempty" jsonschema:"First chapter. Defaults to 1."`
	EndChapter       int    `json:"end_chapter" jsonschema:"Last chapter, inclusive."`
	Concurrency      int    `json:"concurrency,omitempty" jsonschema:"Parallel worker count. Capped by tool config."`
	SegmentSize      int    `json:"segment_size,omitempty" jsonschema:"How many chapter analyses should final reducer treat as one segment. Defaults to 10."`
	WorkerApp        string `json:"worker_app,omitempty" jsonschema:"ADK app/agent used for per-chapter worker sessions."`
	FinalApp         string `json:"final_app,omitempty" jsonschema:"Optional ADK app/agent used for final distillation after chapter workers complete."`
	UserID           string `json:"user_id,omitempty" jsonschema:"Defaults to caller user id."`
	ProjectID        string `json:"project_id,omitempty" jsonschema:"Project/workspace id to copy into worker sessions."`
	ExtraInstruction string `json:"extra_instruction,omitempty" jsonschema:"Additional user requirement for worker/final agents."`
}

type StartResult struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	Total       int    `json:"total"`
	Concurrency int    `json:"concurrency"`
	WorkerApp   string `json:"worker_app"`
	FinalApp    string `json:"final_app,omitempty"`
	Message     string `json:"message"`
}

type GetArgs struct {
	RunID string `json:"run_id"`
}

type GetResult struct {
	Run *RunState `json:"run"`
}

type RunState struct {
	RunID          string      `json:"run_id"`
	Status         string      `json:"status"`
	BookID         string      `json:"book_id"`
	BookName       string      `json:"book_name,omitempty"`
	AnalysisType   string      `json:"analysis_type"`
	StartChapter   int         `json:"start_chapter"`
	EndChapter     int         `json:"end_chapter"`
	Total          int         `json:"total"`
	Completed      int         `json:"completed"`
	Failed         int         `json:"failed"`
	Running        int         `json:"running"`
	Concurrency    int         `json:"concurrency"`
	WorkerApp      string      `json:"worker_app"`
	FinalApp       string      `json:"final_app,omitempty"`
	FinalStatus    string      `json:"final_status,omitempty"`
	FinalSessionID string      `json:"final_session_id,omitempty"`
	Tasks          []TaskState `json:"tasks,omitempty"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
	ErrorMessage   string      `json:"error_message,omitempty"`
}

type TaskState struct {
	Chapter      int    `json:"chapter"`
	Status       string `json:"status"`
	SessionID    string `json:"session_id,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

var (
	runsMu sync.Mutex
	runs   = map[string]*RunState{}
)

func (t *Toolset) Start(ctx tool.Context, args StartArgs) (StartResult, error) {
	if strings.TrimSpace(args.BookID) == "" {
		return StartResult{}, fmt.Errorf("book_id is required")
	}
	if args.StartChapter <= 0 {
		args.StartChapter = 1
	}
	if args.EndChapter < args.StartChapter {
		return StartResult{}, fmt.Errorf("end_chapter must be >= start_chapter")
	}
	if args.AnalysisType == "" {
		args.AnalysisType = "dialogue"
	}
	if args.UserID == "" {
		args.UserID = ctx.UserID()
	}
	if args.ProjectID == "" {
		if v, err := ctx.State().Get("project_id"); err == nil {
			args.ProjectID = fmt.Sprint(v)
		}
	}
	workerApp := strings.TrimSpace(args.WorkerApp)
	if workerApp == "" {
		workerApp = t.cfg.DefaultWorkerApp
	}
	finalApp := strings.TrimSpace(args.FinalApp)
	if finalApp == "" {
		finalApp = t.cfg.DefaultFinalApp
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
	if args.SegmentSize <= 0 {
		args.SegmentSize = 10
	}

	runID := "analysis_" + time.Now().UTC().Format("20060102_150405") + "_" + uuid.NewString()[:8]
	total := args.EndChapter - args.StartChapter + 1
	state := &RunState{
		RunID: runID, Status: statusQueued, BookID: args.BookID, BookName: args.BookName,
		AnalysisType: args.AnalysisType, StartChapter: args.StartChapter, EndChapter: args.EndChapter,
		Total: total, Concurrency: concurrency, WorkerApp: workerApp, FinalApp: finalApp,
		CreatedAt: now(), UpdatedAt: now(),
	}
	for ch := args.StartChapter; ch <= args.EndChapter; ch++ {
		state.Tasks = append(state.Tasks, TaskState{Chapter: ch, Status: statusQueued})
	}
	runsMu.Lock()
	runs[runID] = state
	runsMu.Unlock()

	go t.run(context.Background(), runID, args, workerApp, finalApp, concurrency)

	return StartResult{RunID: runID, Status: statusQueued, Total: total, Concurrency: concurrency, WorkerApp: workerApp, FinalApp: finalApp, Message: "analysis run accepted; use analysis_run_get to check progress"}, nil
}

func (t *Toolset) Get(ctx tool.Context, args GetArgs) (GetResult, error) {
	runsMu.Lock()
	defer runsMu.Unlock()
	st, ok := runs[args.RunID]
	if !ok {
		return GetResult{}, fmt.Errorf("analysis run %q not found", args.RunID)
	}
	cp := *st
	cp.Tasks = append([]TaskState(nil), st.Tasks...)
	return GetResult{Run: &cp}, nil
}

func (t *Toolset) run(ctx context.Context, runID string, args StartArgs, workerApp, finalApp string, concurrency int) {
	t.update(runID, func(st *RunState) { st.Status = statusRunning; st.UpdatedAt = now() })
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for ch := args.StartChapter; ch <= args.EndChapter; ch++ {
		chapter := ch
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			t.runWorker(ctx, runID, args, workerApp, chapter)
		}()
	}
	wg.Wait()
	var failed int
	t.update(runID, func(st *RunState) { failed = st.Failed })
	if failed > 0 {
		t.update(runID, func(st *RunState) {
			st.Status = statusFailed
			st.ErrorMessage = fmt.Sprintf("%d/%d chapter tasks failed", st.Failed, st.Total)
			st.UpdatedAt = now()
		})
		return
	}
	if finalApp != "" {
		t.update(runID, func(st *RunState) { st.FinalStatus = statusRunning; st.UpdatedAt = now() })
		if err := t.runFinal(ctx, runID, args, finalApp); err != nil {
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

func (t *Toolset) runWorker(ctx context.Context, runID string, args StartArgs, workerApp string, chapter int) {
	sessionID := fmt.Sprintf("%s_ch_%04d", runID, chapter)
	t.updateTask(runID, chapter, func(ts *TaskState) { ts.Status = statusRunning; ts.SessionID = sessionID; ts.StartedAt = now() })
	prompt := fmt.Sprintf(`你是章节级对话分析 worker。只处理这一章，不要读取其他章节，不要把长正文返回给主会话。

任务：分析小说章节中的对话写法，提炼可复用的对话 skill 片段。
book_id=%s
book_name=%s
chapter_no=%04d
analysis_type=%s
run_id=%s

要求：
1. 调用小说 MCP 工具读取当前章节。
2. 只分析本章对话，不总结全书。
3. 输出结构化 JSON：dialogue_scene_types、role_relations、techniques、transfer_rules、anti_patterns。
4. 调用 save_skill_batch 保存结果，skill_type 使用 dialogue_chapter_analysis，batch_no 使用章节号 %d，overwrite=true。
5. 最终回复只返回已保存的 batch_no/object_key/简短状态，不要返回章节正文。

额外要求：%s`, args.BookID, args.BookName, chapter, args.AnalysisType, runID, chapter, args.ExtraInstruction)
	err := t.createSessionAndRun(ctx, workerApp, args.UserID, sessionID, args.ProjectID, prompt)
	if err != nil {
		t.updateTask(runID, chapter, func(ts *TaskState) { ts.Status = statusFailed; ts.CompletedAt = now(); ts.ErrorMessage = err.Error() })
		t.update(runID, func(st *RunState) { st.Running--; st.Failed++; st.UpdatedAt = now() })
		return
	}
	t.updateTask(runID, chapter, func(ts *TaskState) { ts.Status = statusCompleted; ts.CompletedAt = now() })
	t.update(runID, func(st *RunState) { st.Running--; st.Completed++; st.UpdatedAt = now() })
}

func (t *Toolset) runFinal(ctx context.Context, runID string, args StartArgs, finalApp string) error {
	sessionID := fmt.Sprintf("%s_final", runID)
	prompt := fmt.Sprintf(`你是对话 skill 汇总器。不要读取章节正文，只读取已经保存的 dialogue_chapter_analysis batch。

book_id=%s
book_name=%s
run_id=%s
chapter_range=%04d-%04d
analysis_type=%s

任务：
1. 调用 list_skill_batches 查询 skill_type=dialogue_chapter_analysis 的逐章分析结果。
2. 必要时调用 get_skill_batch 读取这些小 JSON 结果。
3. 归纳成一个可复用的“如何写对话”skill。
4. 调用 merge_skill_batches 或 save_skill_batch 保存最终结果，skill_type 使用 dialogue_final_skill。
5. 最终回复只返回最终产物 object_key/状态，不要输出长文全文。`, args.BookID, args.BookName, runID, args.StartChapter, args.EndChapter, args.AnalysisType)
	t.update(runID, func(st *RunState) { st.FinalSessionID = sessionID })
	return t.createSessionAndRun(ctx, finalApp, args.UserID, sessionID, args.ProjectID, prompt)
}

func (t *Toolset) createSessionAndRun(ctx context.Context, appName, userID, sessionID, projectID, prompt string) error {
	if appName == "" {
		return fmt.Errorf("appName is required")
	}
	createURL := fmt.Sprintf("%s/apps/%s/users/%s/sessions/%s", strings.TrimRight(t.cfg.RuntimeBaseURL, "/"), url.PathEscape(appName), url.PathEscape(userID), url.PathEscape(sessionID))
	state := map[string]any{}
	if projectID != "" {
		state["project_id"] = projectID
		state["projectId"] = projectID
	}
	if err := t.postJSON(ctx, createURL, map[string]any{"state": state}, nil); err != nil && !strings.Contains(err.Error(), "already") {
		// Some session services return an error for an existing session. Treat
		// already-exists-like responses as non-fatal to support retries.
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
		sort.Slice(st.Tasks, func(i, j int) bool { return st.Tasks[i].Chapter < st.Tasks[j].Chapter })
	}
}
func (t *Toolset) updateTask(runID string, chapter int, fn func(*TaskState)) {
	runsMu.Lock()
	defer runsMu.Unlock()
	if st := runs[runID]; st != nil {
		for i := range st.Tasks {
			if st.Tasks[i].Chapter == chapter {
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
