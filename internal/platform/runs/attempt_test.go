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

func TestRunAllowsOnlyOneActiveAttemptAndRetriesAfterFailure(t *testing.T) {
	svc := NewService(openRunTestDB(t))
	ctx := context.Background()

	run, err := svc.CreateRun(ctx, CreateRunRequest{TenantID: "default", IdempotencyKey: "attempt-replay"})
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
	first, err := svc.CreateAttempt(ctx, CreateAttemptRequest{TenantID: "default", RunID: run.ID})
	if err != nil {
		t.Fatalf("CreateAttempt first: %v", err)
	}
	if _, err := svc.CreateAttempt(ctx, CreateAttemptRequest{TenantID: "default", RunID: run.ID}); err == nil {
		t.Fatal("CreateAttempt accepted a second attempt while the run was queued")
	}

	if _, err := svc.UpdateAttempt(ctx, "default", first.ID, UpdateAttemptRequest{Status: AttemptStatusRunning}); err != nil {
		t.Fatalf("UpdateAttempt running: %v", err)
	}
	if _, err := svc.UpdateRun(ctx, "default", run.ID, UpdateRunRequest{Status: StatusRunning}); err != nil {
		t.Fatalf("UpdateRun running: %v", err)
	}
	finalizer := svc.(ExecutionFinalizer)
	if _, err := finalizer.FinalizeExecution(ctx, FinalizeExecutionRequest{
		TenantID:     "default",
		RunID:        run.ID,
		AttemptID:    first.ID,
		Status:       StatusFailed,
		FailureCode:  "TEST_FAILURE",
		ErrorMessage: "retryable",
	}); err != nil {
		t.Fatalf("FinalizeExecution failed: %v", err)
	}

	retry, err := svc.CreateAttempt(ctx, CreateAttemptRequest{TenantID: "default", RunID: run.ID})
	if err != nil {
		t.Fatalf("CreateAttempt retry: %v", err)
	}
	if retry.AttemptNumber != 2 {
		t.Fatalf("retry attempt number=%d, want 2", retry.AttemptNumber)
	}
}
