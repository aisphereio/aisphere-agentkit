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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adkrest/internal/models"
	"google.golang.org/adk/session"
)

// RuntimeAPIController is the controller for the AISphere Runtime API.
//
// Production text execution is implemented only by the native ADK-Go runtime
// entrypoints in runtime_entry_native.go/runtime_native.go. Realtime execution
// is intentionally isolated in runtime_live.go and must not become a fallback
// for normal text runs.
type RuntimeAPIController struct {
	sseTimeout        time.Duration
	sessionService    session.Service
	memoryService     memory.Service
	artifactService   artifact.Service
	agentLoader       agent.Loader
	pluginConfig      runner.PluginConfig
	autoCreateSession bool
	runtimeConfig     *runtimeconfig.Config
	traceRecorder     runtimetrace.Recorder
	subAgentStore     SubAgentTaskObserveStore
	nativeManager     *sessionnative.Manager
}

// NewRuntimeAPIController creates the controller for the Runtime API.
func NewRuntimeAPIController(sessionService session.Service, memoryService memory.Service, agentLoader agent.Loader, artifactService artifact.Service, sseTimeout time.Duration, pluginConfig runner.PluginConfig, autoCreateSession bool, extras ...any) *RuntimeAPIController {
	var runtimeConfig *runtimeconfig.Config
	var traceRecorder runtimetrace.Recorder
	var subAgentStore SubAgentTaskObserveStore
	var nativeManager *sessionnative.Manager
	for _, extra := range extras {
		switch v := extra.(type) {
		case *runtimeconfig.Config:
			runtimeConfig = v
		case runtimetrace.Recorder:
			traceRecorder = v
		case SubAgentTaskObserveStore:
			subAgentStore = v
		case *sessionnative.Manager:
			nativeManager = v
		}
	}
	return &RuntimeAPIController{
		sseTimeout:        sseTimeout,
		sessionService:    sessionService,
		memoryService:     memoryService,
		artifactService:   artifactService,
		agentLoader:       agentLoader,
		pluginConfig:      pluginConfig,
		autoCreateSession: autoCreateSession,
		runtimeConfig:     runtimeConfig,
		traceRecorder:     traceRecorder,
		subAgentStore:     subAgentStore,
		nativeManager:     nativeManager,
	}
}

func (c *RuntimeAPIController) runtimeContext(ctx context.Context) context.Context {
	if c.runtimeConfig != nil {
		ctx = runtimeconfig.WithConfig(ctx, c.runtimeConfig)
	}
	var recorder runtimetrace.Recorder
	if c.traceRecorder != nil {
		recorder = c.traceRecorder
	}
	if c.subAgentStore != nil {
		storeRecorder := newRuntimeLogSSERecorder(nil, c.subAgentStore)
		if recorder != nil {
			recorder = runtimetrace.NewMultiRecorder(recorder, storeRecorder)
		} else {
			recorder = storeRecorder
		}
	}
	if recorder != nil {
		ctx = runtimetrace.WithRecorder(ctx, recorder)
	}
	return ctx
}

func newRuntimeInvocationID() string {
	return "e-" + uuid.NewString()
}

func (c *RuntimeAPIController) applyPlanMode(req models.RunAgentRequest) models.RunAgentRequest {
	if !strings.EqualFold(strings.TrimSpace(req.RunMode), "plan") {
		return req
	}
	if req.StateDelta == nil {
		req.StateDelta = &map[string]any{}
	}
	delta := *req.StateDelta
	delta["__adk_plan_mode__"] = true
	if req.PlanOptions != nil {
		delta["__adk_plan_options__"] = req.PlanOptions
	}

	control := buildPlanModeControlText(req.PlanOptions)
	if strings.TrimSpace(control) == "" {
		return req
	}
	parts := make([]*genai.Part, 0, len(req.NewMessage.Parts)+1)
	parts = append(parts, &genai.Part{Text: control})
	parts = append(parts, req.NewMessage.Parts...)
	req.NewMessage.Parts = parts
	return req
}

func buildPlanModeControlText(opts map[string]any) string {
	maxParallel := intFromAny(opts["maxParallelAgents"], 8)
	defaultBatch := intFromAny(opts["defaultBatchSize"], 1)
	maxContext := intFromAny(opts["maxContextChars"], 30000)
	return fmt.Sprintf(`[ADK_PLAN_MODE]
当前用户已手动开启“计划模式”。这是运行时控制指令，不是用户正文。

执行原则：
1. 先规划，再执行；不要直接把大量章节/文件正文读入当前会话。
2. 面对长任务（多章节、多文件、批量分析、全书提炼）时，先生成任务计划，说明范围、粒度、并发数、产物位置和风险。
3. 优先使用异步任务、analysis_run、子 Agent、worker/reducer/distiller 或可用的任务启动工具；主会话只保留 run_id、进度和最终产物引用。
4. 正文只允许进入 worker/子 Agent 的短会话；每个 worker 处理一章或少量章节，并把结果保存为 artifact/MinIO/MCP 产物。
5. 如果当前工具集中没有并行任务启动工具，不要在主会话串行读取大量上下文；先输出可执行计划并说明缺少的工具。
6. 默认并发上限：%d；默认章节批大小：%d；单轮上下文目标上限：%d 字符。
[/ADK_PLAN_MODE]`, maxParallel, defaultBatch, maxContext)
}

func intFromAny(v any, fallback int) int {
	switch t := v.(type) {
	case int:
		if t > 0 {
			return t
		}
	case int64:
		if t > 0 {
			return int(t)
		}
	case float64:
		if t > 0 {
			return int(t)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func flashErrorEvent(rc *http.ResponseController, rw http.ResponseWriter, origError error, writeTimeout time.Duration) error {
	if _, err := fmt.Fprint(rw, "event: error\n"); err != nil {
		return fmt.Errorf("write error event: %w", err)
	}
	safeErrorJSON, err := json.Marshal(map[string]string{"error": origError.Error()})
	if err != nil {
		return fmt.Errorf("marshal error event: %w", err)
	}
	return flashEvent(rc, rw, string(safeErrorJSON), writeTimeout)
}

func flashEvent(rc *http.ResponseController, rw http.ResponseWriter, data string, writeTimeout time.Duration) error {
	return flashEventWithID(rc, rw, "", data, writeTimeout)
}

func flashEventWithID(rc *http.ResponseController, rw http.ResponseWriter, id, data string, writeTimeout time.Duration) error {
	return flashNamedEventWithID(rc, rw, "", id, data, writeTimeout)
}

func flashNamedEventWithID(rc *http.ResponseController, rw http.ResponseWriter, eventName, id, data string, writeTimeout time.Duration) error {
	if err := setSSEWriteDeadline(rc, writeTimeout); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if eventName != "" {
		if _, err := fmt.Fprintf(rw, "event: %s\n", eventName); err != nil {
			return fmt.Errorf("write event name: %w", err)
		}
	}
	if id != "" {
		if _, err := fmt.Fprintf(rw, "id: %s\n", id); err != nil {
			return fmt.Errorf("write event id: %w", err)
		}
	}
	if _, err := fmt.Fprintf(rw, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	if err := rc.Flush(); err != nil {
		return fmt.Errorf("flush event: %w", err)
	}
	if err := clearSSEWriteDeadline(rc); err != nil {
		return fmt.Errorf("clear write deadline: %w", err)
	}
	return nil
}

func setSSEHeaders(rw http.ResponseWriter) {
	rw.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	rw.Header().Set("X-Accel-Buffering", "no")
}

func flushSSE(rc *http.ResponseController, writeTimeout time.Duration) error {
	if err := setSSEWriteDeadline(rc, writeTimeout); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if err := rc.Flush(); err != nil {
		return fmt.Errorf("flush event: %w", err)
	}
	if err := clearSSEWriteDeadline(rc); err != nil {
		return fmt.Errorf("clear write deadline: %w", err)
	}
	return nil
}

func setSSEWriteDeadline(rc *http.ResponseController, writeTimeout time.Duration) error {
	if writeTimeout <= 0 {
		return clearSSEWriteDeadline(rc)
	}
	return rc.SetWriteDeadline(time.Now().Add(writeTimeout))
}

func clearSSEWriteDeadline(rc *http.ResponseController) error {
	return rc.SetWriteDeadline(time.Time{})
}

func (c *RuntimeAPIController) validateSessionExists(ctx context.Context, appName, userID, sessionID string) error {
	_, err := c.sessionService.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return newStatusError(fmt.Errorf("failed to get session: %w", err), http.StatusNotFound)
	}
	return nil
}

func decodeRequestBody(req *http.Request) (models.RunAgentRequest, error) {
	var runAgentRequest models.RunAgentRequest
	d := json.NewDecoder(req.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&runAgentRequest); err != nil {
		return runAgentRequest, newStatusError(fmt.Errorf("failed to decode request: %w", err), http.StatusBadRequest)
	}
	return runAgentRequest, nil
}
