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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/uploads"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adkrest/internal/models"
	"google.golang.org/adk/server/adkrest/internal/resumable"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/envmanagertool"
)

// RuntimeAPIController is the controller for the Runtime API.
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
	runStore          *resumable.Store
	uploadService     uploads.Service
	subAgentStore     SubAgentTaskObserveStore
	nativeManager     *sessionnative.Manager
}

type requestScopedAgentLoader interface {
	LoadAgentForRequest(ctx context.Context, name, sessionID string) (agent.Agent, error)
}

// NewRuntimeAPIController creates the controller for the Runtime API.
func NewRuntimeAPIController(sessionService session.Service, memoryService memory.Service, agentLoader agent.Loader, artifactService artifact.Service, sseTimeout time.Duration, pluginConfig runner.PluginConfig, autoCreateSession bool, extras ...any) *RuntimeAPIController {
	var runtimeConfig *runtimeconfig.Config
	var traceRecorder runtimetrace.Recorder
	var runStore *resumable.Store
	var uploadService uploads.Service
	var subAgentStore SubAgentTaskObserveStore
	var nativeManager *sessionnative.Manager
	for _, extra := range extras {
		switch v := extra.(type) {
		case *runtimeconfig.Config:
			runtimeConfig = v
		case runtimetrace.Recorder:
			traceRecorder = v
		case *resumable.Store:
			runStore = v
		case uploads.Service:
			uploadService = v
		case SubAgentTaskObserveStore:
			subAgentStore = v
		case *sessionnative.Manager:
			nativeManager = v
		}
	}
	return &RuntimeAPIController{sessionService: sessionService, memoryService: memoryService, agentLoader: agentLoader, artifactService: artifactService, sseTimeout: sseTimeout, pluginConfig: pluginConfig, autoCreateSession: autoCreateSession, runtimeConfig: runtimeConfig, traceRecorder: traceRecorder, runStore: runStore, uploadService: uploadService, subAgentStore: subAgentStore, nativeManager: nativeManager}
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

const maxAutoMountUploadBytes int64 = 256 << 20 // 256 MiB

func (c *RuntimeAPIController) prepareProjectWorkspace(ctx context.Context, req models.RunAgentRequest) (models.RunAgentRequest, error) {
	projectID := c.resolveRunProjectID(ctx, req)
	if projectID == "" {
		return req, nil
	}
	if req.StateDelta == nil {
		req.StateDelta = &map[string]any{}
	}
	delta := *req.StateDelta
	delta["project_id"] = projectID
	delta["projectId"] = projectID
	if _, ok := delta["__project_context__"]; !ok {
		delta["__project_context__"] = map[string]any{"project_id": projectID, "mounted_by": "runtime", "mounted_for_agent": req.AppName}
	}
	if err := c.mountProjectUploadsIntoSessionArtifacts(ctx, req, projectID); err != nil {
		// Do not block the model run because of a best-effort workspace sync, but
		// leave a server-side breadcrumb. The user can still attach manually from
		// the Upload tab.
		log.Printf("failed to auto-mount project uploads for project %s session %s: %v", projectID, req.SessionId, err)
	}
	return req, nil
}

func (c *RuntimeAPIController) resolveRunProjectID(ctx context.Context, req models.RunAgentRequest) string {
	for _, v := range []string{req.ProjectId, req.ProjectID} {
		if projectID := strings.TrimSpace(v); projectID != "" {
			return projectID
		}
	}
	if req.StateDelta != nil {
		if projectID := projectIDFromMap(*req.StateDelta); projectID != "" {
			return projectID
		}
	}
	if c.sessionService == nil || strings.TrimSpace(req.SessionId) == "" {
		return ""
	}
	resp, err := c.sessionService.Get(ctx, &session.GetRequest{AppName: req.AppName, UserID: req.UserId, SessionID: req.SessionId})
	if err != nil || resp == nil || resp.Session == nil || resp.Session.State() == nil {
		return ""
	}
	for _, key := range []string{"project_id", "projectId"} {
		value, err := resp.Session.State().Get(key)
		if err == nil {
			if projectID, ok := value.(string); ok && strings.TrimSpace(projectID) != "" {
				return strings.TrimSpace(projectID)
			}
		}
	}
	return ""
}

func projectIDFromMap(m map[string]any) string {
	for _, key := range []string{"project_id", "projectId"} {
		if value, ok := m[key]; ok {
			if projectID, ok := value.(string); ok && strings.TrimSpace(projectID) != "" {
				return strings.TrimSpace(projectID)
			}
		}
	}
	if raw, ok := m["__project_context__"]; ok {
		if ctxMap, ok := raw.(map[string]any); ok {
			if projectID, ok := ctxMap["project_id"].(string); ok && strings.TrimSpace(projectID) != "" {
				return strings.TrimSpace(projectID)
			}
		}
	}
	return ""
}

func (c *RuntimeAPIController) mountProjectUploadsIntoSessionArtifacts(ctx context.Context, req models.RunAgentRequest, projectID string) error {
	if c.uploadService == nil || c.artifactService == nil || projectID == "" || req.SessionId == "" {
		return nil
	}
	principal := auth.FromContext(ctx)
	tenantID := principal.TenantID
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "default"
	}
	items, err := c.uploadService.List(ctx, uploads.ListFilter{
		TenantID:  tenantID,
		UserID:    req.UserId,
		ProjectID: projectID,
		Status:    uploads.StatusActive,
		Limit:     200,
	})
	if err != nil {
		return err
	}
	for _, item := range items {
		artifactName := filepath.Base(strings.TrimSpace(item.OriginalName))
		if artifactName == "" || artifactName == "." || artifactName == ".." {
			artifactName = item.ID + ".bin"
		}
		if _, err := c.artifactService.Load(ctx, &artifact.LoadRequest{AppName: req.AppName, UserID: req.UserId, SessionID: req.SessionId, FileName: artifactName}); err == nil {
			continue
		}
		reader, upload, err := c.uploadService.Open(ctx, tenantID, item.ID)
		if err != nil {
			return err
		}
		limited := &io.LimitedReader{R: reader, N: maxAutoMountUploadBytes + 1}
		data, readErr := io.ReadAll(limited)
		closeErr := reader.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) > maxAutoMountUploadBytes {
			log.Printf("skip auto-mount upload %s (%s): %d bytes exceeds %d", item.ID, item.OriginalName, len(data), maxAutoMountUploadBytes)
			continue
		}
		mimeType := item.MIMEType
		if upload != nil && upload.MIMEType != "" {
			mimeType = upload.MIMEType
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if _, err := c.artifactService.Save(ctx, &artifact.SaveRequest{
			AppName:   req.AppName,
			UserID:    req.UserId,
			SessionID: req.SessionId,
			FileName:  artifactName,
			Part:      &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: data}},
		}); err != nil {
			return err
		}
	}
	return nil
}

// RunAgent executes a non-streaming agent run for a given session and message.
func (c *RuntimeAPIController) RunHandler(rw http.ResponseWriter, req *http.Request) error {
	runAgentRequest, err := decodeRequestBody(req)
	if err != nil {
		return err
	}
	if err := c.validateRunInputPolicy(runAgentRequest); err != nil {
		return err
	}
	if c.nativeManager != nil && c.nativeManager.Enabled() {
		events, err := c.runNativeAgent(aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header), runAgentRequest)
		if err != nil {
			return err
		}
		EncodeJSONResponse(events, http.StatusOK, rw)
		return nil
	}
	sessionEvents, err := c.runAgent(aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header), runAgentRequest)
	if err != nil {
		return err
	}
	var events []models.Event
	for _, event := range sessionEvents {
		events = append(events, models.FromSessionEvent(*event))
	}
	EncodeJSONResponse(events, http.StatusOK, rw)
	return nil
}

// RunAgent executes a non-streaming agent run for a given session and message.
func (c *RuntimeAPIController) runAgent(ctx context.Context, runAgentRequest models.RunAgentRequest) ([]*session.Event, error) {
	runAgentRequest = c.applyPlanMode(runAgentRequest)
	ctx = c.runtimeContext(ctx)
	err := c.validateSessionExists(ctx, runAgentRequest.AppName, runAgentRequest.UserId, runAgentRequest.SessionId)
	if err != nil {
		return nil, err
	}

	runAgentRequest, err = c.prepareProjectWorkspace(ctx, runAgentRequest)
	if err != nil {
		return nil, err
	}

	r, rCfg, err := c.getRunner(ctx, runAgentRequest)
	if err != nil {
		return nil, err
	}

	var opts []runner.RunOption
	if runAgentRequest.InvocationId == "" {
		runAgentRequest.InvocationId = newRuntimeInvocationID()
	}
	opts = append(opts, runner.WithInvocationID(runAgentRequest.InvocationId))
	if runAgentRequest.StateDelta != nil {
		opts = append(opts, runner.WithStateDelta(*runAgentRequest.StateDelta))
	}
	resp := r.Run(ctx, runAgentRequest.UserId, runAgentRequest.SessionId, &runAgentRequest.NewMessage, *rCfg, opts...)

	var events []*session.Event
	for event, err := range resp {
		if err != nil {
			return nil, newStatusError(fmt.Errorf("failed to run agent: %w", err), http.StatusInternalServerError)
		}
		events = append(events, event)
	}
	return events, nil
}

// RunSSEHandler executes an agent run and streams the resulting events using Server-Sent Events (SSE).
func (c *RuntimeAPIController) RunSSEHandler(rw http.ResponseWriter, req *http.Request) {
	// SSE responses can stay open for a long time while the model/tool is running.
	// Clear any server-wide absolute write deadline here, and only apply a
	// per-write deadline when we actually write/flush an SSE frame.
	rc := http.NewResponseController(rw)
	if err := clearSSEWriteDeadline(rc); err != nil {
		http.Error(rw, "failed to clear write deadline: "+err.Error(), http.StatusInternalServerError)
		return
	}

	runAgentRequest, err := decodeRequestBody(req)
	if err != nil {
		http.Error(rw, "failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	runAgentRequest = c.applyPlanMode(runAgentRequest)
	if err := c.validateRunInputPolicy(runAgentRequest); err != nil {
		status := http.StatusRequestEntityTooLarge
		if se, ok := err.(interface{ Status() int }); ok {
			status = se.Status()
		}
		http.Error(rw, err.Error(), status)
		return
	}

	if c.nativeManager != nil && c.nativeManager.Enabled() {
		c.runNativeAgentSSE(rw, req, runAgentRequest)
		return
	}

	ctx := c.runtimeContext(aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header))
	err = c.validateSessionExists(ctx, runAgentRequest.AppName, runAgentRequest.UserId, runAgentRequest.SessionId)
	if err != nil {
		http.Error(rw, "failed to find the session: "+err.Error(), http.StatusNotFound)
		return
	}

	runAgentRequest, err = c.prepareProjectWorkspace(ctx, runAgentRequest)
	if err != nil {
		http.Error(rw, "failed to prepare project workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	r, rCfg, err := c.getRunner(ctx, runAgentRequest)
	if err != nil {
		http.Error(rw, "failed to get runner: "+err.Error(), http.StatusInternalServerError)
		return
	}

	opts := []runner.RunOption{}
	invocationID := runAgentRequest.InvocationId
	if invocationID == "" {
		invocationID = newRuntimeInvocationID()
		runAgentRequest.InvocationId = invocationID
	}
	opts = append(opts, runner.WithInvocationID(invocationID))
	if runAgentRequest.StateDelta != nil {
		opts = append(opts, runner.WithStateDelta(*runAgentRequest.StateDelta))
	}

	if c.runStore != nil {
		runID, err := c.runStore.Start(runAgentRequest, func(runCtx context.Context, emit func(string) error) error {
			runCtx = c.runtimeContext(runCtx)
			runtimeLogRecorder := newRuntimeLogSSERecorder(emit, c.subAgentStore)
			if c.traceRecorder != nil {
				runCtx = runtimetrace.WithRecorder(runCtx, runtimetrace.NewMultiRecorder(c.traceRecorder, runtimeLogRecorder))
			} else {
				runCtx = runtimetrace.WithRecorder(runCtx, runtimeLogRecorder)
			}
			resp := r.Run(runCtx, runAgentRequest.UserId, runAgentRequest.SessionId, &runAgentRequest.NewMessage, *rCfg, opts...)
			for event, err := range resp {
				if err != nil {
					return fmt.Errorf("failed to run agent: %w", err)
				}
				if event == nil {
					continue
				}
				marshalledData, err := json.Marshal(models.FromSessionEvent(*event))
				if err != nil {
					return fmt.Errorf("marshal event: %w", err)
				}
				if err := emit(string(marshalledData)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			http.Error(rw, "failed to start resumable run: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// rw.Header().Set("X-ADK-Run-ID", runID)
		c.streamResumableRun(rw, req, runID, "0-0", invocationID)
		return
	}

	// Flush as soon as possible so the client doesn't drop connection.
	// Add the headers after the error handling to avoid wrong content type.
	setSSEHeaders(rw)
	rw.Header().Set("X-ADK-Invocation-ID", invocationID)
	if err := flushSSE(rc, c.sseTimeout); err != nil {
		http.Error(rw, "failed to flush headers", http.StatusInternalServerError)
		return
	}
	metadata, err := json.Marshal(map[string]any{
		"adkRunMetadata": true,
		"invocationId":   invocationID,
		"cursor":         "",
	})
	if err != nil {
		http.Error(rw, "failed to marshal run metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := flashEventWithID(rc, rw, "", string(metadata), c.sseTimeout); err != nil {
		log.Printf("failed to write invocation metadata %s: %v", invocationID, err)
		return
	}

	resp := r.Run(ctx, runAgentRequest.UserId, runAgentRequest.SessionId, &runAgentRequest.NewMessage, *rCfg, opts...)

	for event, err := range resp {
		if err != nil {
			err := flashErrorEvent(rc, rw, err, c.sseTimeout)
			// The error is returned only when we cannot communicate with the client
			// Exit the handler as connection is closed.
			if err != nil {
				log.Printf("failed to flash error event: %v", err)
				return
			}
			continue
		}
		if event == nil {
			continue
		}
		// Skip reporting error if it fails to marshal to the client (to avoid recursive error reporting).
		marshalledData, err := json.Marshal(models.FromSessionEvent(*event))
		if err != nil {
			log.Printf("failed to marshal event: %v", err)
			return
		}
		err = flashEvent(rc, rw, string(marshalledData), c.sseTimeout)
		if err != nil {
			log.Printf("failed to flash event: %v", err)
			return
		}
	}
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

// SubAgentTaskEventsHandler returns persisted sub-agent task runtime events for a session.
func (c *RuntimeAPIController) SubAgentTaskEventsHandler(rw http.ResponseWriter, req *http.Request) error {
	return c.runtimeEventsHandler(rw, req, true)
}

// RuntimeEventsHandler returns persisted runtime observation events for a session.
func (c *RuntimeAPIController) RuntimeEventsHandler(rw http.ResponseWriter, req *http.Request) error {
	return c.runtimeEventsHandler(rw, req, false)
}

func (c *RuntimeAPIController) runtimeEventsHandler(rw http.ResponseWriter, req *http.Request, subAgentOnlyDefault bool) error {
	if c.subAgentStore == nil {
		EncodeJSONResponse(map[string]any{"events": []any{}}, http.StatusOK, rw)
		return nil
	}
	q := req.URL.Query()
	appName := strings.TrimSpace(q.Get("app_name"))
	userID := strings.TrimSpace(q.Get("user_id"))
	sessionID := strings.TrimSpace(q.Get("session_id"))
	scope := strings.ToLower(strings.TrimSpace(q.Get("scope")))
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	events := c.subAgentStore.List(req.Context(), appName, userID, sessionID)
	if events == nil {
		events = []map[string]any{}
	}
	if scope == "subagent" || (scope == "" && subAgentOnlyDefault) {
		filtered := make([]map[string]any, 0, len(events))
		for _, event := range events {
			if strings.HasPrefix(runtimeStoredEventType(event), "subagent.task.") {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	} else if scope == "errors" || scope == "error" {
		filtered := make([]map[string]any, 0, len(events))
		for _, event := range events {
			if runtimeStoredEventIsError(event) {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}
	EncodeJSONResponse(map[string]any{"events": events}, http.StatusOK, rw)
	return nil
}

func runtimeStoredEventType(event map[string]any) string {
	if event == nil {
		return ""
	}
	if s := strings.TrimSpace(anyString(event["event_type"])); s != "" {
		return s
	}
	if s := strings.TrimSpace(anyString(event["eventType"])); s != "" {
		return s
	}
	if data, ok := event["data"].(map[string]any); ok {
		if s := strings.TrimSpace(anyString(data["event_type"])); s != "" {
			return s
		}
		if s := strings.TrimSpace(anyString(data["eventType"])); s != "" {
			return s
		}
	}
	return strings.TrimSpace(anyString(event["type"]))
}

func runtimeStoredEventIsError(event map[string]any) bool {
	if event == nil {
		return false
	}
	eventType := runtimeStoredEventType(event)
	switch eventType {
	case runtimetrace.EventInvocationFailed,
		runtimetrace.EventAgentError,
		runtimetrace.EventModelCallError,
		runtimetrace.EventToolError,
		runtimetrace.EventSkillError,
		runtimetrace.EventSubAgentTaskFailed:
		return true
	}
	if strings.Contains(strings.ToLower(eventType), ".error") || strings.Contains(strings.ToLower(eventType), ".failed") {
		return true
	}
	if data, ok := event["data"].(map[string]any); ok {
		if strings.TrimSpace(anyString(data["error"])) != "" ||
			strings.TrimSpace(anyString(data["error_message"])) != "" ||
			strings.TrimSpace(anyString(data["error_code"])) != "" {
			return true
		}
	}
	return false
}

// BusinessLogStreamHandler streams real business logs (Docker/K8s/file/journal) as SSE.
// It is intentionally separate from runtime trace logs: this endpoint shows application/container logs requested by the user.
func (c *RuntimeAPIController) BusinessLogStreamHandler(rw http.ResponseWriter, req *http.Request) {
	rc := http.NewResponseController(rw)
	if err := clearSSEWriteDeadline(rc); err != nil {
		http.Error(rw, "failed to clear write deadline: "+err.Error(), http.StatusInternalServerError)
		return
	}
	setSSEHeaders(rw)
	if err := flushSSE(rc, c.sseTimeout); err != nil {
		http.Error(rw, "failed to flush headers", http.StatusInternalServerError)
		return
	}

	q := req.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	follow := q.Get("follow") == "1" || q.Get("follow") == "true" || q.Get("follow") == "yes"
	blogReq := envmanagertool.BusinessLogRequest{
		EnvironmentID: q.Get("environment_id"),
		Kind:          q.Get("kind"),
		Container:     q.Get("container"),
		Namespace:     q.Get("namespace"),
		Pod:           q.Get("pod"),
		K8sContainer:  q.Get("k8s_container"),
		Path:          q.Get("path"),
		Unit:          q.Get("unit"),
		Tail:          tail,
		Follow:        follow,
	}

	configPath := c.defaultBusinessLogEnvConfigPath()
	if configPath == "" {
		_ = flashNamedEventWithID(rc, rw, "business.log.error", "", `{"type":"business.log.error","message":"env config path not available"}`, c.sseTimeout)
		return
	}
	cfg := envmanagertool.Config{
		ConfigPath:            configPath,
		DefaultSafetyMode:     envmanagertool.SafetyModeSafeApproval,
		DefaultFreedomLevel:   envmanagertool.FreedomF2,
		DefaultMaxOutputBytes: 64 * 1024,
		DefaultTimeoutSeconds: 0,
		AllowLocal:            false,
		DryRunDefault:         false,
	}

	emit := func(event envmanagertool.BusinessLogEvent) error {
		data, err := envmanagertool.EncodeBusinessLogEvent(event)
		if err != nil {
			return err
		}
		return flashNamedEventWithID(rc, rw, event.Type, "", data, c.sseTimeout)
	}
	if err := envmanagertool.StreamBusinessLogs(req.Context(), cfg, blogReq, emit); err != nil {
		log.Printf("business log stream failed: %v", err)
	}
}

func (c *RuntimeAPIController) defaultBusinessLogEnvConfigPath() string {
	if c.runtimeConfig == nil {
		return filepath.Clean("agents/env_manager/env/environments.example.json")
	}
	appsRoot := c.runtimeConfig.Builder.AppsRoot
	if appsRoot == "" {
		appsRoot = "./agents"
	}
	return filepath.Clean(filepath.Join(appsRoot, "env_manager", "env", "environments.example.json"))
}

func (c *RuntimeAPIController) ResumeRunSSEHandler(rw http.ResponseWriter, req *http.Request) {
	if c.runStore == nil {
		http.Error(rw, "resumable runs are not enabled", http.StatusNotFound)
		return
	}
	runID := req.URL.Query().Get("runId")
	if runID == "" {
		runID = req.URL.Query().Get("run_id")
	}
	if runID == "" {
		http.Error(rw, "runId is required", http.StatusBadRequest)
		return
	}
	exists, err := c.runStore.Exists(req.Context(), runID)
	if err != nil {
		http.Error(rw, "failed to check run: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(rw, "run not found or expired", http.StatusNotFound)
		return
	}

	cursor := req.URL.Query().Get("cursor")
	invocationID, err := c.runStore.InvocationID(req.Context(), runID)
	if err != nil {
		http.Error(rw, "failed to load run metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	c.streamResumableRun(rw, req, runID, cursor, invocationID)
}

func (c *RuntimeAPIController) CancelRunHandler(rw http.ResponseWriter, req *http.Request) error {
	if c.runStore == nil {
		return newStatusError(fmt.Errorf("resumable runs are not enabled"), http.StatusNotFound)
	}
	runID := req.URL.Query().Get("runId")
	if runID == "" {
		runID = req.URL.Query().Get("run_id")
	}
	if runID == "" {
		return newStatusError(fmt.Errorf("runId is required"), http.StatusBadRequest)
	}
	EncodeJSONResponse(map[string]bool{"canceled": c.runStore.Cancel(runID)}, http.StatusOK, rw)
	return nil
}

func (c *RuntimeAPIController) streamResumableRun(rw http.ResponseWriter, req *http.Request, runID, cursor, invocationID string) {
	rc := http.NewResponseController(rw)
	if err := clearSSEWriteDeadline(rc); err != nil {
		http.Error(rw, "failed to clear write deadline: "+err.Error(), http.StatusInternalServerError)
		return
	}
	setSSEHeaders(rw)
	rw.Header().Set("X-ADK-Run-ID", runID)
	if invocationID != "" {
		rw.Header().Set("X-ADK-Invocation-ID", invocationID)
	}

	if err := flushSSE(rc, c.sseTimeout); err != nil {
		http.Error(rw, "failed to flush headers", http.StatusInternalServerError)
		return
	}

	// 关键：SSE 第一帧直接把 runID/cursor 发给前端。
	// 不要只依赖 X-ADK-Run-ID 响应头，否则刷新恢复链路不稳定。
	metadata, err := json.Marshal(map[string]any{
		"adkRunMetadata": true,
		"runId":          runID,
		"invocationId":   invocationID,
		"cursor":         cursor,
	})
	if err != nil {
		http.Error(rw, "failed to marshal run metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 用普通 message 事件发，不用 named event，兼容现有前端 SSE parser。
	if err := flashEventWithID(rc, rw, "", string(metadata), c.sseTimeout); err != nil {
		log.Printf("failed to write run metadata %s: %v", runID, err)
		return
	}

	err = c.runStore.Stream(req.Context(), runID, cursor, func(msg resumable.Message) error {
		switch msg.Kind {
		case resumable.KindError:
			return flashNamedEventWithID(rc, rw, "error", msg.ID, msg.Data, c.sseTimeout)
		case resumable.KindDone:
			return flashEventWithID(rc, rw, msg.ID, `{"adkRunDone":true}`, c.sseTimeout)
		case resumable.KindHeartbeat:
			return flashEventWithID(rc, rw, "", `{"adkRunHeartbeat":true}`, c.sseTimeout)
		default:
			return flashEventWithID(rc, rw, msg.ID, msg.Data, c.sseTimeout)
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("failed to stream resumable run %s: %v", runID, err)
	}
}

func flashErrorEvent(rc *http.ResponseController, rw http.ResponseWriter, origError error, writeTimeout time.Duration) error {
	_, err := fmt.Fprintf(rw, "event: error\n")
	if err != nil {
		return fmt.Errorf("write error event: %w", err)
	}
	safeErrorJSON, err := json.Marshal(map[string]string{"error": origError.Error()})
	if err != nil {
		// Skip reporting error if it fails to marshal to the client (to avoid recursive error reporting).
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

func (c *RuntimeAPIController) getRunner(ctx context.Context, req models.RunAgentRequest) (*runner.Runner, *agent.RunConfig, error) {
	var curAgent agent.Agent
	var err error
	if loader, ok := c.agentLoader.(requestScopedAgentLoader); ok {
		curAgent, err = loader.LoadAgentForRequest(ctx, req.AppName, req.SessionId)
	} else {
		curAgent, err = c.agentLoader.LoadAgent(req.AppName)
	}
	if err != nil {
		return nil, nil, newStatusError(fmt.Errorf("failed to load agent: %w", err), http.StatusInternalServerError)
	}

	r, err := runner.New(runner.Config{
		AppName:           req.AppName,
		Agent:             curAgent,
		SessionService:    c.sessionService,
		MemoryService:     c.memoryService,
		ArtifactService:   c.artifactService,
		PluginConfig:      c.pluginConfig,
		AutoCreateSession: c.autoCreateSession,
	},
	)
	if err != nil {
		return nil, nil, newStatusError(fmt.Errorf("failed to create runner: %w", err), http.StatusInternalServerError)
	}

	streamingMode := agent.StreamingModeNone
	if req.Streaming {
		streamingMode = agent.StreamingModeSSE
	}
	return r, &agent.RunConfig{
		StreamingMode: streamingMode,
	}, nil
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

func (c *RuntimeAPIController) RunLiveHandler(rw http.ResponseWriter, req *http.Request) error {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	q := req.URL.Query()
	appName := q.Get("appName")
	if appName == "" {
		appName = q.Get("app_name")
	}
	userID := q.Get("userId")
	if userID == "" {
		userID = q.Get("user_id")
	}
	sessionID := q.Get("sessionId")
	if sessionID == "" {
		sessionID = q.Get("session_id")
	}

	if appName == "" || userID == "" || sessionID == "" {
		return fmt.Errorf("appName, userId, and sessionId are required")
	}

	ws, err := upgrader.Upgrade(rw, req, nil)
	if err != nil {
		return fmt.Errorf("failed to upgrade to websocket: %w", err)
	}
	defer func() {
		_ = ws.Close()
	}()

	sendClose := func(code int, reason string) {
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
		_ = ws.SetReadDeadline(time.Now().Add(time.Second))
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				break
			}
		}
	}

	ctx := c.runtimeContext(aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header))
	r, _, err := c.getRunner(ctx, models.RunAgentRequest{AppName: appName, UserId: userID, SessionId: sessionID})
	if err != nil {
		closeReason := err.Error()
		if _, loadErr := c.agentLoader.LoadAgent(appName); loadErr != nil {
			closeReason = fmt.Sprintf("agent %s not found for original error: %v", appName, err)
		}
		log.Printf("Failed to get runner for app %s: %v", appName, err)
		sendClose(websocket.CloseInternalServerErr, closeReason)
		return nil
	}

	// Read from Runner and write back to client over the WebSocket
	liveSession, eventIter, err := r.RunLive(req.Context(), userID, sessionID, agent.LiveRunConfig{
		MaxLLMCalls:              100, // Reasonable default
		ResponseModalities:       []genai.Modality{genai.ModalityAudio},
		InputAudioTranscription:  &genai.AudioTranscriptionConfig{},
		OutputAudioTranscription: &genai.AudioTranscriptionConfig{},
	})
	if err != nil {
		log.Printf("RunLive failed for app %s: %v", appName, err)
		sendClose(websocket.CloseInternalServerErr, err.Error())
		return nil
	}
	defer func() {
		_ = liveSession.Close()
	}()

	// Spawning goroutine for reading from the client over WebSocket and pushing it to Runner
	go func() {
		defer func() {
			_ = liveSession.Close()
		}()
		for {
			messageType, p, err := ws.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("WebSocket read error for app %s: %v", appName, err)
				}
				break
			}

			if messageType == websocket.BinaryMessage {
				if err := liveSession.Send(agent.LiveRequest{
					RealtimeInput: &genai.Blob{
						MIMEType: "audio/pcm;rate=16000",
						Data:     p,
					},
				}); err != nil {
					log.Printf("Failed to send binary data to Gemini for app %s: %v", appName, err)
					break
				}
			} else if messageType == websocket.TextMessage {
				var apiReq models.LiveRequest
				if err := json.Unmarshal(p, &apiReq); err != nil {
					log.Printf("Failed to unmarshal client message for app %s: %v", appName, err)
					continue
				}

				if apiReq.Close {
					break
				}

				liveReq := agent.LiveRequest{
					Content: apiReq.Content,
				}

				if apiReq.ActivityStart != nil {
					liveReq.RealtimeInput = apiReq.ActivityStart
				} else if apiReq.ActivityEnd != nil {
					liveReq.RealtimeInput = apiReq.ActivityEnd
				} else if apiReq.Blob != nil {
					liveReq.RealtimeInput = &genai.Blob{
						MIMEType: apiReq.Blob.MIMEType,
						Data:     apiReq.Blob.Data,
					}
				}

				if err := liveSession.Send(liveReq); err != nil {
					log.Printf("Failed to send message to Gemini for app %s: %v", appName, err)
					break
				}
			}
		}
	}()

	for event, err := range eventIter {
		if err != nil {
			log.Printf("RunLive failed: %v\n", err)
			_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
			break
		}

		err = ws.WriteJSON(models.FromSessionEvent(*event))
		if err != nil {
			break
		}
	}

	return nil
}
