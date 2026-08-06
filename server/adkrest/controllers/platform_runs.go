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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"google.golang.org/adk/internal/platform/auth"
	platformruns "google.golang.org/adk/internal/platform/runs"
)

const (
	runtimeEventPollInterval = 500 * time.Millisecond
	runtimeEventHeartbeat    = 15 * time.Second
)

// PlatformRunsAPIController exposes Runtime execution facts. Snapshot, Attempt,
// and Event endpoints are intentionally read-only; only Runtime may create them.
type PlatformRunsAPIController struct {
	service platformruns.Service
}

func NewPlatformRunsAPIController(service platformruns.Service) *PlatformRunsAPIController {
	return &PlatformRunsAPIController{service: service}
}

func (c *PlatformRunsAPIController) ListRunsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	runs, err := c.service.ListRuns(req.Context(), platformruns.ListRunsFilter{
		TenantID:  p.TenantID,
		ProjectID: q.Get("project_id"),
		AgentID:   q.Get("agent_id"),
		AppName:   q.Get("app_name"),
		UserID:    q.Get("user_id"),
		SessionID: q.Get("session_id"),
		Status:    q.Get("status"),
		Limit:     limit,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(runs, http.StatusOK, rw)
}

// CreateRunHandler remains during route migration only. The public router no
// longer registers it because execution facts must be created by Runtime.
func (c *PlatformRunsAPIController) CreateRunHandler(rw http.ResponseWriter, req *http.Request) {
	http.Error(rw, "runs are created by the Runtime execution engine", http.StatusMethodNotAllowed)
}

func (c *PlatformRunsAPIController) GetRunHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["run_id"]
	run, err := c.service.GetRun(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(run, http.StatusOK, rw)
}

// UpdateRunHandler remains during route migration only. Runtime owns all status
// transitions and terminal facts.
func (c *PlatformRunsAPIController) UpdateRunHandler(rw http.ResponseWriter, req *http.Request) {
	http.Error(rw, "run state is managed by the Runtime execution engine", http.StatusMethodNotAllowed)
}

func (c *PlatformRunsAPIController) GetExecutionSnapshotHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	runID := mux.Vars(req)["run_id"]
	run, err := c.service.GetRun(req.Context(), p.TenantID, runID)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	if run.SnapshotID == "" {
		http.Error(rw, "execution snapshot not created", http.StatusNotFound)
		return
	}
	snapshot, err := c.service.GetExecutionSnapshot(req.Context(), p.TenantID, run.SnapshotID)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(snapshot, http.StatusOK, rw)
}

func (c *PlatformRunsAPIController) ListAttemptsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	runID := mux.Vars(req)["run_id"]
	attempts, err := c.service.ListAttempts(req.Context(), p.TenantID, runID)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(attempts, http.StatusOK, rw)
}

func (c *PlatformRunsAPIController) ListEventsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	runID := mux.Vars(req)["run_id"]
	after, err := parseRuntimeEventCursor(req)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	events, err := c.service.ListEvents(req.Context(), p.TenantID, runID, after, limit)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(events, http.StatusOK, rw)
}

// StreamEventsHandler projects the durable RuntimeEvent ledger to SSE. The
// RuntimeEvent sequence is the SSE id, allowing reconnect through either the
// Last-Event-ID header or the after query parameter after process restarts.
func (c *PlatformRunsAPIController) StreamEventsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	principal := auth.FromContext(req.Context())
	runID := strings.TrimSpace(mux.Vars(req)["run_id"])
	if runID == "" {
		http.Error(rw, "run_id is required", http.StatusBadRequest)
		return
	}
	if _, err := c.service.GetRun(req.Context(), principal.TenantID, runID); err != nil {
		writePlatformError(rw, err)
		return
	}
	cursor, err := parseRuntimeEventCursor(req)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	setSSEHeaders(rw)
	rw.Header().Set("X-AISphere-Run-ID", runID)
	rw.Header().Set("X-Accel-Buffering", "no")
	responseController := http.NewResponseController(rw)
	if err := flushSSE(responseController, 0); err != nil {
		return
	}

	pollTicker := time.NewTicker(runtimeEventPollInterval)
	heartbeatTicker := time.NewTicker(runtimeEventHeartbeat)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		terminal, nextCursor, err := c.flushRuntimeEventBatch(
			req,
			responseController,
			rw,
			principal.TenantID,
			runID,
			cursor,
		)
		if err != nil {
			return
		}
		cursor = nextCursor
		if terminal {
			return
		}

		select {
		case <-req.Context().Done():
			return
		case <-pollTicker.C:
			continue
		case <-heartbeatTicker.C:
			if _, err := fmt.Fprint(rw, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := flushSSE(responseController, 0); err != nil {
				return
			}
		}
	}
}

func (c *PlatformRunsAPIController) flushRuntimeEventBatch(
	req *http.Request,
	responseController *http.ResponseController,
	rw http.ResponseWriter,
	tenantID string,
	runID string,
	cursor uint64,
) (bool, uint64, error) {
	events, err := c.service.ListEvents(req.Context(), tenantID, runID, cursor, 200)
	if err != nil {
		return false, cursor, err
	}
	for _, runtimeEvent := range events {
		encoded, err := json.Marshal(runtimeEvent)
		if err != nil {
			return false, cursor, err
		}
		if err := flashEventWithID(
			responseController,
			rw,
			strconv.FormatUint(runtimeEvent.Sequence, 10),
			string(encoded),
			0,
		); err != nil {
			return false, cursor, err
		}
		cursor = runtimeEvent.Sequence
		if isTerminalRuntimeEvent(runtimeEvent.EventType) {
			return true, cursor, nil
		}
	}
	return false, cursor, nil
}

func parseRuntimeEventCursor(req *http.Request) (uint64, error) {
	raw := strings.TrimSpace(req.URL.Query().Get("after"))
	if lastEventID := strings.TrimSpace(req.Header.Get("Last-Event-ID")); lastEventID != "" {
		raw = lastEventID
	}
	if raw == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Runtime event cursor %q", raw)
	}
	return cursor, nil
}

func isTerminalRuntimeEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "run.completed", "run.failed", "run.cancelled":
		return true
	default:
		return false
	}
}

func (c *PlatformRunsAPIController) ListStepsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	runID := mux.Vars(req)["run_id"]
	steps, err := c.service.ListSteps(req.Context(), p.TenantID, runID)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(steps, http.StatusOK, rw)
}

// CreateStepHandler is no longer registered. RuntimeEvent replaces mutable
// coarse-grained steps as the execution timeline.
func (c *PlatformRunsAPIController) CreateStepHandler(rw http.ResponseWriter, req *http.Request) {
	http.Error(rw, "run steps are deprecated; Runtime writes append-only events", http.StatusMethodNotAllowed)
}

// UpdateStepHandler is no longer registered.
func (c *PlatformRunsAPIController) UpdateStepHandler(rw http.ResponseWriter, req *http.Request) {
	http.Error(rw, "run steps are deprecated; Runtime writes append-only events", http.StatusMethodNotAllowed)
}

func writePlatformError(rw http.ResponseWriter, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(rw, err.Error(), http.StatusInternalServerError)
}
