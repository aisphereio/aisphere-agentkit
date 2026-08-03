package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/internal/sessionworkerclient"
	"google.golang.org/adk/server/adkrest/internal/models"
)

func (c *RuntimeAPIController) runNativeAgent(ctx context.Context, req models.RunAgentRequest) ([]models.Event, error) {
	req = c.applyPlanMode(req)
	if err := c.validateSessionExists(ctx, req.AppName, req.UserId, req.SessionId); err != nil {
		return nil, err
	}
	lease, worker, err := c.ensureNativeWorker(ctx, req)
	if err != nil {
		return nil, err
	}
	message := contentText(req.NewMessage)
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("native sandbox worker currently requires a text message")
	}
	invocationID := req.InvocationId
	if invocationID == "" {
		invocationID = newRuntimeInvocationID()
	}
	resp, err := worker.SendMessage(ctx, sessionworkerclient.MessageRequest{
		RunID:   invocationID,
		Message: message,
		Metadata: map[string]interface{}{
			"appName":      req.AppName,
			"userId":       req.UserId,
			"sessionId":    req.SessionId,
			"projectId":    firstNativeNonEmpty(req.ProjectId, req.ProjectID),
			"sandboxId":    lease.Sandbox.SandboxID,
			"snapshotId":   lease.SnapshotID,
			"native":       true,
			"invocationId": invocationID,
		},
	})
	if err != nil {
		return nil, err
	}
	runID := invocationID
	if resp != nil && strings.TrimSpace(resp.RunID) != "" {
		runID = strings.TrimSpace(resp.RunID)
	}
	events, err := worker.Events(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]models.Event, 0, len(events))
	for _, ev := range events {
		out = append(out, nativeWorkerEventToModelEvent(ev, invocationID))
	}
	return out, nil
}

func (c *RuntimeAPIController) runNativeAgentSSE(rw http.ResponseWriter, req *http.Request, runReq models.RunAgentRequest) {
	rc := http.NewResponseController(rw)
	setSSEHeaders(rw)
	invocationID := runReq.InvocationId
	if invocationID == "" {
		invocationID = newRuntimeInvocationID()
		runReq.InvocationId = invocationID
	}
	rw.Header().Set("X-ADK-Invocation-ID", invocationID)
	if err := flushSSE(rc, c.sseTimeout); err != nil {
		http.Error(rw, "failed to flush headers", http.StatusInternalServerError)
		return
	}
	metadata, _ := json.Marshal(map[string]any{"adkRunMetadata": true, "invocationId": invocationID, "cursor": "", "nativeSandbox": true})
	if err := flashEventWithID(rc, rw, "", string(metadata), c.sseTimeout); err != nil {
		log.Printf("failed to write native invocation metadata %s: %v", invocationID, err)
		return
	}

	ctx := c.runtimeContext(aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header))
	if err := c.validateSessionExists(ctx, runReq.AppName, runReq.UserId, runReq.SessionId); err != nil {
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	lease, worker, err := c.ensureNativeWorker(ctx, runReq)
	if err != nil {
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	message := contentText(runReq.NewMessage)
	if strings.TrimSpace(message) == "" {
		_ = flashErrorEvent(rc, rw, fmt.Errorf("native sandbox worker currently requires a text message"), c.sseTimeout)
		return
	}
	resp, err := worker.SendMessage(ctx, sessionworkerclient.MessageRequest{
		RunID:   invocationID,
		Message: message,
		Metadata: map[string]interface{}{
			"appName":      runReq.AppName,
			"userId":       runReq.UserId,
			"sessionId":    runReq.SessionId,
			"projectId":    firstNativeNonEmpty(runReq.ProjectId, runReq.ProjectID),
			"sandboxId":    lease.Sandbox.SandboxID,
			"snapshotId":   lease.SnapshotID,
			"native":       true,
			"invocationId": invocationID,
		},
	})
	if err != nil {
		_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
		return
	}
	runID := invocationID
	if resp != nil && strings.TrimSpace(resp.RunID) != "" {
		runID = strings.TrimSpace(resp.RunID)
	}
	idleLimit := 2
	if c.runtimeConfig != nil && c.runtimeConfig.Skills.AIHub.Sandbox.EventIdleSeconds > 0 {
		idleLimit = c.runtimeConfig.Skills.AIHub.Sandbox.EventIdleSeconds
	}
	idle := 0
	for {
		events, err := worker.Events(ctx, runID)
		if err != nil {
			_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
			return
		}
		if len(events) == 0 {
			idle++
			if idle >= idleLimit {
				break
			}
			continue
		}
		idle = 0
		for _, ev := range events {
			modelEvent := nativeWorkerEventToModelEvent(ev, invocationID)
			data, err := json.Marshal(modelEvent)
			if err != nil {
				_ = flashErrorEvent(rc, rw, err, c.sseTimeout)
				return
			}
			if err := flashEvent(rc, rw, string(data), c.sseTimeout); err != nil {
				log.Printf("failed to flash native worker event: %v", err)
				return
			}
		}
		if nativeRunComplete(events) {
			break
		}
	}
	_ = flashEventWithID(rc, rw, "", `{"adkRunDone":true,"nativeSandbox":true}`, c.sseTimeout)
}

func (c *RuntimeAPIController) ensureNativeWorker(ctx context.Context, req models.RunAgentRequest) (*sessionnative.SessionLease, *sessionworkerclient.Client, error) {
	if c.nativeManager == nil || !c.nativeManager.Enabled() {
		return nil, nil, fmt.Errorf("native sandbox session manager is disabled")
	}
	lease, err := c.nativeManager.EnsureSession(ctx, sessionnative.CreateSessionRequest{
		AppName:   req.AppName,
		UserID:    req.UserId,
		SessionID: req.SessionId,
		ProjectID: firstNativeNonEmpty(req.ProjectId, req.ProjectID),
		AgentID:   req.AppName,
		State:     stateDeltaToMap(req.StateDelta),
		Reuse:     true,
	})
	if err != nil {
		return nil, nil, err
	}
	worker, err := c.nativeManager.WorkerClient(lease)
	if err != nil {
		return nil, nil, err
	}
	return lease, worker, nil
}

func nativeWorkerEventToModelEvent(ev sessionworkerclient.Event, invocationID string) models.Event {
	author := "agentkit-session-worker"
	role := "model"
	if ev.Type == "user_message" {
		author = "user"
		role = "user"
	}
	text := ev.Content
	if strings.TrimSpace(text) == "" && ev.Data != nil {
		b, _ := json.Marshal(ev.Data)
		text = string(b)
	}
	return models.Event{
		ID:           "native-" + uuid.NewString(),
		InvocationID: invocationID,
		Author:       author,
		Content:      &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}},
		TurnComplete: ev.Type == "assistant_message" || ev.Type == "error" || ev.Type == "run_done",
		Actions:      models.EventActions{StateDelta: map[string]any{"__native_worker_event__": map[string]any{"type": ev.Type, "runId": ev.RunID, "createdAt": ev.CreatedAt}}},
	}
}

func nativeRunComplete(events []sessionworkerclient.Event) bool {
	for _, ev := range events {
		switch ev.Type {
		case "assistant_message", "error", "run_done", "assistant_done":
			return true
		}
	}
	return false
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
