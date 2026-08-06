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
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"google.golang.org/adk/internal/platform/auth"
	platformruns "google.golang.org/adk/internal/platform/runs"
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

func (c *PlatformRunsAPIController) CreateRunHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		ProjectID      string `json:"project_id"`
		ConversationID string `json:"conversation_id"`
		AppName        string `json:"app_name"`
		AgentID        string `json:"agent_id"`
		AgentRevision  string `json:"agent_revision"`
		UserID         string `json:"user_id"`
		SessionID      string `json:"session_id"`
		Status         string `json:"status"`
		TriggerType    string `json:"trigger_type"`
		IdempotencyKey string `json:"idempotency_key"`
		InputSummary   string `json:"input_summary"`
		ModelRef       string `json:"model_ref"`
		TraceID        string `json:"trace_id"`
		MetadataJSON   string `json:"metadata_json"`
	}
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}
	userID := body.UserID
	if userID == "" {
		userID = p.UserID
	}
	run, err := c.service.CreateRun(req.Context(), platformruns.CreateRunRequest{
		TenantID:       p.TenantID,
		ProjectID:      body.ProjectID,
		ConversationID: body.ConversationID,
		AppName:        body.AppName,
		AgentID:        body.AgentID,
		AgentRevision:  body.AgentRevision,
		UserID:         userID,
		PrincipalID:    p.UserID,
		SessionID:      body.SessionID,
		Status:         body.Status,
		TriggerType:    body.TriggerType,
		IdempotencyKey: body.IdempotencyKey,
		InputSummary:   body.InputSummary,
		ModelRef:       body.ModelRef,
		TraceID:        body.TraceID,
		MetadataJSON:   body.MetadataJSON,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(run, http.StatusCreated, rw)
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

func (c *PlatformRunsAPIController) UpdateRunHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["run_id"]
	var body struct {
		Status       string  `json:"status"`
		FailureCode  string  `json:"failure_code"`
		ErrorMessage string  `json:"error_message"`
		MetadataJSON *string `json:"metadata_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	run, err := c.service.UpdateRun(req.Context(), p.TenantID, id, platformruns.UpdateRunRequest{
		Status:       body.Status,
		FailureCode:  body.FailureCode,
		ErrorMessage: body.ErrorMessage,
		MetadataJSON: body.MetadataJSON,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(run, http.StatusOK, rw)
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
	after, _ := strconv.ParseUint(req.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	events, err := c.service.ListEvents(req.Context(), p.TenantID, runID, after, limit)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(events, http.StatusOK, rw)
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

func (c *PlatformRunsAPIController) CreateStepHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	runID := mux.Vars(req)["run_id"]
	var body struct {
		Kind        string `json:"kind"`
		Status      string `json:"status"`
		PayloadJSON string `json:"payload_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	step, err := c.service.CreateStep(req.Context(), platformruns.CreateStepRequest{
		TenantID:    p.TenantID,
		RunID:       runID,
		Kind:        body.Kind,
		Status:      body.Status,
		PayloadJSON: body.PayloadJSON,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(step, http.StatusCreated, rw)
}

func (c *PlatformRunsAPIController) UpdateStepHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform run service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	stepID := mux.Vars(req)["step_id"]
	var body struct {
		Status       string  `json:"status"`
		ErrorMessage string  `json:"error_message"`
		PayloadJSON  *string `json:"payload_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	step, err := c.service.UpdateStep(req.Context(), p.TenantID, stepID, platformruns.UpdateStepRequest{
		Status:       body.Status,
		ErrorMessage: body.ErrorMessage,
		PayloadJSON:  body.PayloadJSON,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(step, http.StatusOK, rw)
}

func writePlatformError(rw http.ResponseWriter, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}
	if errors.Is(err, platformruns.ErrLegacyPostgresExecutionFacts) {
		http.Error(rw, err.Error(), http.StatusNotImplemented)
		return
	}
	http.Error(rw, err.Error(), http.StatusInternalServerError)
}
