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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/modelruntime"
	"google.golang.org/adk/internal/runtimeexecutor"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/server/adkrest/internal/models"
)

func (c *RuntimeAPIController) runNativeAgent(ctx context.Context, req models.RunAgentRequest) ([]models.Event, error) {
	req = c.applyPlanMode(req)
	if err := c.validateSessionExists(ctx, req.AppName, req.UserId, req.SessionId); err != nil {
		return nil, err
	}
	if req.InvocationId == "" {
		req.InvocationId = newRuntimeInvocationID()
	}
	lease, err := c.ensureNativeLease(ctx, req)
	if err != nil {
		return nil, err
	}
	return c.runNativeAgentGo(ctx, req, lease)
}

func (c *RuntimeAPIController) runNativeAgentSSE(rw http.ResponseWriter, req *http.Request, runReq models.RunAgentRequest) {
	invocationID := runReq.InvocationId
	if invocationID == "" {
		invocationID = newRuntimeInvocationID()
		runReq.InvocationId = invocationID
	}
	ctx := c.runtimeContext(aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header))
	if err := c.validateSessionExists(ctx, runReq.AppName, runReq.UserId, runReq.SessionId); err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}
	lease, err := c.ensureNativeLease(ctx, runReq)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	facts, err := c.beginNativeExecutionFacts(ctx, runReq, lease, invocationID)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rc := http.NewResponseController(rw)
	setSSEHeaders(rw)
	rw.Header().Set("X-ADK-Invocation-ID", invocationID)
	rw.Header().Set("X-AISphere-Run-ID", facts.Run.ID)
	if err := flushSSE(rc, c.sseTimeout); err != nil {
		facts.fail(ctx, c.executionFactService(), "SSE_HEADER_FAILED", err)
		http.Error(rw, "failed to flush headers", http.StatusInternalServerError)
		return
	}
	metadata, _ := json.Marshal(map[string]any{
		"adkRunMetadata": true,
		"runId":          facts.Run.ID,
		"snapshotId":     facts.Snapshot.ID,
		"attemptId":      facts.Attempt.ID,
		"invocationId":   invocationID,
		"cursor":         "0",
		"nativeSandbox":  true,
		"runtimeEngine":  "adk-go",
	})
	if err := flashEventWithID(rc, rw, "", string(metadata), c.sseTimeout); err != nil {
		facts.fail(ctx, c.executionFactService(), "SSE_METADATA_FAILED", err)
		log.Printf("failed to write native invocation metadata %s: %v", invocationID, err)
		return
	}
	c.runNativeAgentGoSSE(rc, rw, ctx, runReq, lease, invocationID, facts)
}

func (c *RuntimeAPIController) ensureNativeLease(ctx context.Context, req models.RunAgentRequest) (*sessionnative.SessionLease, error) {
	if c.nativeManager == nil || !c.nativeManager.Enabled() {
		return nil, fmt.Errorf("native sandbox session manager is disabled")
	}
	lease, err := c.nativeManager.EnsureSession(ctx, sessionnative.CreateSessionRequest{
		AppName:           req.AppName,
		UserID:            req.UserId,
		SessionID:         req.SessionId,
		ProjectID:         firstNativeNonEmpty(req.ProjectId, req.ProjectID),
		AgentID:           req.AppName,
		Version:           req.Version,
		ApprovalConfirmed: req.ApprovalConfirmed,
		ApprovedTools:     req.ApprovedTools,
		State:             stateDeltaToMap(req.StateDelta),
		Reuse:             true,
	})
	if err != nil {
		return nil, err
	}
	return lease, nil
}

func (c *RuntimeAPIController) runNativeAgentGo(ctx context.Context, req models.RunAgentRequest, lease *sessionnative.SessionLease) ([]models.Event, error) {
	if lease == nil || lease.Plan == nil {
		return nil, fmt.Errorf("native Go runner requires a Hub runtime plan")
	}
	message := contentText(req.NewMessage)
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("native Go runner requires a text message")
	}
	invocationID := req.InvocationId
	if invocationID == "" {
		invocationID = newRuntimeInvocationID()
	}
	facts, err := c.beginNativeExecutionFacts(ctx, req, lease, invocationID)
	if err != nil {
		return nil, err
	}
	service := c.executionFactService()

	llm, _, err := modelruntime.NewModel(ctx, lease.Plan.Model)
	if err != nil {
		wrapped := fmt.Errorf("resolve native Go runner model: %w", err)
		facts.fail(ctx, service, "MODEL_RESOLVE_FAILED", wrapped)
		return nil, wrapped
	}
	toolRegistry, err := c.nativeManager.ToolRegistryForLease(lease)
	if err != nil {
		facts.fail(ctx, service, "TOOL_REGISTRY_FAILED", err)
		return nil, err
	}
	toolsets, err := c.nativeManager.ToolsetsForLease(ctx, lease)
	if err != nil {
		wrapped := fmt.Errorf("resolve native Go runner skills: %w", err)
		facts.fail(ctx, service, "SKILL_RESOLVE_FAILED", wrapped)
		return nil, wrapped
	}
	executor := runtimeexecutor.Executor{
		Model: llm, SessionService: c.sessionService, ToolRegistry: toolRegistry,
		Toolsets:          toolsets,
		AutoCreateSession: c.autoCreateSession,
	}
	var out []models.Event
	for event, runErr := range executor.Run(ctx, runtimeexecutor.RunRequest{
		Plan: lease.Plan, AppName: req.AppName, UserID: req.UserId, SessionID: req.SessionId,
		Message: message, InvocationID: invocationID, StateDelta: stateDeltaToMap(req.StateDelta),
	}) {
		if runErr != nil {
			wrapped := fmt.Errorf("native Go runner failed: %w", runErr)
			facts.fail(ctx, service, "AGENT_RUN_FAILED", wrapped)
			return nil, wrapped
		}
		if event == nil {
			continue
		}
		modelEvent := models.FromSessionEvent(*event)
		if _, err := facts.appendModelEvent(ctx, service, modelEvent); err != nil {
			facts.fail(ctx, service, "EVENT_LEDGER_FAILED", err)
			return nil, err
		}
		out = append(out, modelEvent)
		if eventErr := runtimeexecutor.EventError(event); eventErr != nil {
			facts.fail(ctx, service, runtimeexecutor.FailureCode(eventErr), eventErr)
			return nil, eventErr
		}
	}
	if err := facts.succeed(ctx, service); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *RuntimeAPIController) runNativeAgentGoSSE(
	rc *http.ResponseController,
	rw http.ResponseWriter,
	ctx context.Context,
	req models.RunAgentRequest,
	lease *sessionnative.SessionLease,
	invocationID string,
	facts *executionFactContext,
) {
	service := c.executionFactService()
	if lease == nil || lease.Plan == nil {
		err := fmt.Errorf("native Go runner requires a Hub runtime plan")
		facts.fail(ctx, service, "SNAPSHOT_MISSING", err)
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	message := contentText(req.NewMessage)
	if strings.TrimSpace(message) == "" {
		err := fmt.Errorf("native Go runner requires a text message")
		facts.fail(ctx, service, "INPUT_INVALID", err)
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	llm, _, err := modelruntime.NewModel(ctx, lease.Plan.Model)
	if err != nil {
		wrapped := fmt.Errorf("resolve native Go runner model: %w", err)
		facts.fail(ctx, service, "MODEL_RESOLVE_FAILED", wrapped)
		_ = flashErrorEvent(rc, rw, wrapped, c.sseTimeout)
		return
	}
	toolRegistry, err := c.nativeManager.ToolRegistryForLease(lease)
	if err != nil {
		facts.fail(ctx, service, "TOOL_REGISTRY_FAILED", err)
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	toolsets, err := c.nativeManager.ToolsetsForLease(ctx, lease)
	if err != nil {
		wrapped := fmt.Errorf("resolve native Go runner skills: %w", err)
		facts.fail(ctx, service, "SKILL_RESOLVE_FAILED", wrapped)
		_ = flashErrorEvent(rc, rw, wrapped, c.sseTimeout)
		return
	}
	executor := runtimeexecutor.Executor{
		Model: llm, SessionService: c.sessionService, ToolRegistry: toolRegistry,
		Toolsets:          toolsets,
		AutoCreateSession: c.autoCreateSession,
	}
	for event, runErr := range executor.Run(ctx, runtimeexecutor.RunRequest{
		Plan: lease.Plan, AppName: req.AppName, UserID: req.UserId, SessionID: req.SessionId,
		Message: message, InvocationID: invocationID, Streaming: true, StateDelta: stateDeltaToMap(req.StateDelta),
	}) {
		if runErr != nil {
			wrapped := fmt.Errorf("native Go runner failed: %w", runErr)
			facts.fail(ctx, service, "AGENT_RUN_FAILED", wrapped)
			_ = flashErrorEvent(rc, rw, wrapped, c.sseTimeout)
			return
		}
		if event == nil {
			continue
		}
		modelEvent := models.FromSessionEvent(*event)
		storedEvent, err := facts.appendModelEvent(ctx, service, modelEvent)
		if err != nil {
			facts.fail(ctx, service, "EVENT_LEDGER_FAILED", err)
			_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
			return
		}
		if eventErr := runtimeexecutor.EventError(event); eventErr != nil {
			facts.fail(ctx, service, runtimeexecutor.FailureCode(eventErr), eventErr)
			_ = flashErrorEvent(rc, rw, eventErr, c.sseTimeout)
			return
		}
		data, err := json.Marshal(modelEvent)
		if err != nil {
			facts.fail(ctx, service, "EVENT_MARSHAL_FAILED", err)
			_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
			return
		}
		if err := flashEventWithID(rc, rw, strconv.FormatUint(storedEvent.Sequence, 10), string(data), c.sseTimeout); err != nil {
			facts.fail(ctx, service, "SSE_WRITE_FAILED", err)
			log.Printf("failed to flash native Go runner event: %v", err)
			return
		}
	}
	if err := facts.succeed(ctx, service); err != nil {
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	done, _ := json.Marshal(map[string]any{
		"adkRunDone":    true,
		"runId":         facts.Run.ID,
		"runtimeEngine": "adk-go",
	})
	_ = flashEventWithID(rc, rw, "", string(done), c.sseTimeout)
}

func contentText(content genai.Content) string {
	var parts []string
	for _, part := range content.Parts {
		if part != nil && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func stateDeltaToMap(delta *map[string]any) map[string]any {
	if delta == nil {
		return nil
	}
	return *delta
}

func firstNativeNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
