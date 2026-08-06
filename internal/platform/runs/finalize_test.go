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

package runs

import (
	"context"
	"testing"
)

func TestFinalizeExecutionAtomicallyPersistsTerminalFacts(t *testing.T) {
	svc := NewService(openRunTestDB(t))
	ctx := context.Background()

	run, err := svc.CreateRun(ctx, CreateRunRequest{TenantID: "default", IdempotencyKey: "finalize-success"})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := svc.CreateExecutionSnapshot(ctx, CreateExecutionSnapshotRequest{
		TenantID:         "default",
		RunID:            run.ID,
		SourceSpecDigest: "sha256:source",
		SnapshotJSON:     `{"agent":{"id":"agent-1"}}`,
	}); err != nil {
		t.Fatalf("CreateExecutionSnapshot: %v", err)
	}
	attempt, err := svc.CreateAttempt(ctx, CreateAttemptRequest{TenantID: "default", RunID: run.ID})
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	if _, err := svc.UpdateAttempt(ctx, "default", attempt.ID, UpdateAttemptRequest{Status: AttemptStatusRunning}); err != nil {
		t.Fatalf("UpdateAttempt running: %v", err)
	}
	if _, err := svc.UpdateRun(ctx, "default", run.ID, UpdateRunRequest{Status: StatusRunning}); err != nil {
		t.Fatalf("UpdateRun running: %v", err)
	}
	if _, err := svc.AppendEvent(ctx, AppendEventRequest{
		TenantID:  "default",
		RunID:     run.ID,
		AttemptID: attempt.ID,
		EventType: "attempt.started",
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	finalizer, ok := svc.(ExecutionFinalizer)
	if !ok {
		t.Fatal("run service does not implement ExecutionFinalizer")
	}
	terminal, err := finalizer.FinalizeExecution(ctx, FinalizeExecutionRequest{
		TenantID:  "default",
		RunID:     run.ID,
		AttemptID: attempt.ID,
		Status:    StatusSucceeded,
		TraceID:   "trace-finalize",
	})
	if err != nil {
		t.Fatalf("FinalizeExecution: %v", err)
	}
	if terminal.EventType != "run.completed" || terminal.Sequence != 3 {
		t.Fatalf("terminal event=%+v, want run.completed sequence 3", terminal)
	}

	storedRun, err := svc.GetRun(ctx, "default", run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if storedRun.Status != StatusSucceeded || storedRun.FinishedAt == nil {
		t.Fatalf("run was not finalized: %+v", storedRun)
	}
	storedAttempt, err := svc.GetAttempt(ctx, "default", attempt.ID)
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if storedAttempt.Status != AttemptStatusSucceeded || storedAttempt.FinishedAt == nil {
		t.Fatalf("attempt was not finalized: %+v", storedAttempt)
	}

	events, err := svc.ListEvents(ctx, "default", run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 || events[1].EventType != "attempt.completed" || events[2].EventType != "run.completed" {
		t.Fatalf("unexpected terminal event sequence: %+v", events)
	}

	idempotent, err := finalizer.FinalizeExecution(ctx, FinalizeExecutionRequest{
		TenantID:  "default",
		RunID:     run.ID,
		AttemptID: attempt.ID,
		Status:    StatusSucceeded,
	})
	if err != nil {
		t.Fatalf("FinalizeExecution idempotent: %v", err)
	}
	if idempotent.ID != terminal.ID {
		t.Fatalf("idempotent finalization event=%q, want %q", idempotent.ID, terminal.ID)
	}
	eventsAfterRetry, err := svc.ListEvents(ctx, "default", run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListEvents after idempotent finalize: %v", err)
	}
	if len(eventsAfterRetry) != len(events) {
		t.Fatalf("idempotent finalization appended duplicate events: %d -> %d", len(events), len(eventsAfterRetry))
	}
}
