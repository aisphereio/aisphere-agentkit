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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	platformruns "google.golang.org/adk/internal/platform/runs"
)

func TestStreamEventsHandlerResumesFromLastEventID(t *testing.T) {
	service := newRuntimeEventStreamTestService(t)
	ctx := context.Background()

	run, err := service.CreateRun(ctx, platformruns.CreateRunRequest{
		TenantID:       "default",
		IdempotencyKey: "stream-resume",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := service.CreateExecutionSnapshot(ctx, platformruns.CreateExecutionSnapshotRequest{
		TenantID:         "default",
		RunID:            run.ID,
		SourceSpecDigest: "sha256:source",
		SnapshotJSON:     `{"agent":{"id":"agent-1"}}`,
	}); err != nil {
		t.Fatalf("CreateExecutionSnapshot: %v", err)
	}
	attempt, err := service.CreateAttempt(ctx, platformruns.CreateAttemptRequest{
		TenantID: "default",
		RunID:    run.ID,
	})
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	if _, err := service.UpdateAttempt(ctx, "default", attempt.ID, platformruns.UpdateAttemptRequest{
		Status: platformruns.AttemptStatusRunning,
	}); err != nil {
		t.Fatalf("UpdateAttempt running: %v", err)
	}
	if _, err := service.UpdateRun(ctx, "default", run.ID, platformruns.UpdateRunRequest{
		Status: platformruns.StatusRunning,
	}); err != nil {
		t.Fatalf("UpdateRun running: %v", err)
	}
	for _, eventType := range []string{"run.created", "agent.event"} {
		if _, err := service.AppendEvent(ctx, platformruns.AppendEventRequest{
			TenantID:  "default",
			RunID:     run.ID,
			AttemptID: attempt.ID,
			EventType: eventType,
		}); err != nil {
			t.Fatalf("AppendEvent %s: %v", eventType, err)
		}
	}
	finalizer := service.(platformruns.ExecutionFinalizer)
	if _, err := finalizer.FinalizeExecution(ctx, platformruns.FinalizeExecutionRequest{
		TenantID:  "default",
		RunID:     run.ID,
		AttemptID: attempt.ID,
		Status:    platformruns.StatusSucceeded,
	}); err != nil {
		t.Fatalf("FinalizeExecution: %v", err)
	}

	controller := NewPlatformRunsAPIController(service)
	request := httptest.NewRequest(http.MethodGet, "/platform/runs/"+run.ID+"/events/stream", nil)
	request.Header.Set("Last-Event-ID", "2")
	request = mux.SetURLVars(request, map[string]string{"run_id": run.ID})
	recorder := httptest.NewRecorder()

	controller.StreamEventsHandler(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("StreamEventsHandler status=%d body=%s", response.StatusCode, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "id: 1\n") || strings.Contains(body, "id: 2\n") {
		t.Fatalf("stream replayed events before cursor: %s", body)
	}
	if !strings.Contains(body, "id: 3\n") || !strings.Contains(body, "id: 4\n") {
		t.Fatalf("stream did not replay terminal events after cursor: %s", body)
	}
	if !strings.Contains(body, `"event_type":"run.completed"`) {
		t.Fatalf("stream did not include terminal run event: %s", body)
	}
}

func TestStreamEventsHandlerRejectsInvalidCursor(t *testing.T) {
	service := newRuntimeEventStreamTestService(t)
	ctx := context.Background()
	run, err := service.CreateRun(ctx, platformruns.CreateRunRequest{TenantID: "default"})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	controller := NewPlatformRunsAPIController(service)
	request := httptest.NewRequest(http.MethodGet, "/platform/runs/"+run.ID+"/events/stream?after=bad", nil)
	request = mux.SetURLVars(request, map[string]string{"run_id": run.ID})
	recorder := httptest.NewRecorder()

	controller.StreamEventsHandler(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("StreamEventsHandler status=%d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func newRuntimeEventStreamTestService(t *testing.T) platformruns.Service {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := platformruns.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return platformruns.NewService(db)
}
