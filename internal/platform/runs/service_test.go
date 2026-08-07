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
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRunExecutionFactLifecycle(t *testing.T) {
	db := openRunTestDB(t)
	svc := NewService(db)
	ctx := context.Background()

	run, err := svc.CreateRun(ctx, CreateRunRequest{
		TenantID:       "default",
		ProjectID:      "project-1",
		AgentID:        "agent-1",
		AgentRevision:  "12",
		UserID:         "admin",
		PrincipalID:    "user:admin",
		SessionID:      "session-1",
		IdempotencyKey: "request-1",
		TraceID:        "trace-1",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.ID == "" || run.Status != StatusPreparing {
		t.Fatalf("unexpected run: %+v", run)
	}

	idempotent, err := svc.CreateRun(ctx, CreateRunRequest{
		TenantID:       "default",
		IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatalf("CreateRun idempotent: %v", err)
	}
	if idempotent.ID != run.ID {
		t.Fatalf("idempotent CreateRun ID=%q, want %q", idempotent.ID, run.ID)
	}

	snapshot, err := svc.CreateExecutionSnapshot(ctx, CreateExecutionSnapshotRequest{
		TenantID:         "default",
		RunID:            run.ID,
		SourceSpecDigest: "sha256:source-spec",
		AgentID:          "agent-1",
		AgentRevision:    "12",
		ModelRevision:    "model-revision-4",
		ResolverVersion:  "hub-resolver/v1",
		RuntimeEngine:    "adk-go",
		SnapshotJSON: `{
			"tools": [],
			"connection": {"credentialRef": "secret://github/user-1"},
			"agent": {"revision": "12", "id": "agent-1"}
		}`,
	})
	if err != nil {
		t.Fatalf("CreateExecutionSnapshot: %v", err)
	}
	if snapshot.ID == "" || snapshot.SnapshotDigest == "" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.SchemaVersion != ExecutionSnapshotSchemaV1 {
		t.Fatalf("snapshot schema=%q, want %q", snapshot.SchemaVersion, ExecutionSnapshotSchemaV1)
	}

	sameSnapshot, err := svc.CreateExecutionSnapshot(ctx, CreateExecutionSnapshotRequest{
		TenantID:         "default",
		RunID:            run.ID,
		SourceSpecDigest: "sha256:source-spec",
		AgentID:          "agent-1",
		AgentRevision:    "12",
		SnapshotJSON:     snapshot.SnapshotJSON,
	})
	if err != nil {
		t.Fatalf("CreateExecutionSnapshot idempotent: %v", err)
	}
	if sameSnapshot.ID != snapshot.ID {
		t.Fatalf("idempotent snapshot ID=%q, want %q", sameSnapshot.ID, snapshot.ID)
	}

	attempt, err := svc.CreateAttempt(ctx, CreateAttemptRequest{
		TenantID:        "default",
		RunID:           run.ID,
		RuntimeBuild:    "runtime-test",
		CompilerVersion: "compiler/v1",
	})
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	if attempt.AttemptNumber != 1 || attempt.Status != AttemptStatusQueued {
		t.Fatalf("unexpected attempt: %+v", attempt)
	}

	firstEvent, err := svc.AppendEvent(ctx, AppendEventRequest{
		TenantID:    "default",
		RunID:       run.ID,
		AttemptID:   attempt.ID,
		EventType:   "attempt.queued",
		PayloadJSON: `{"reason":"new_run"}`,
		TraceID:     "trace-1",
	})
	if err != nil {
		t.Fatalf("AppendEvent first: %v", err)
	}
	secondEvent, err := svc.AppendEvent(ctx, AppendEventRequest{
		TenantID:  "default",
		RunID:     run.ID,
		AttemptID: attempt.ID,
		EventType: "attempt.started",
		TraceID:   "trace-1",
	})
	if err != nil {
		t.Fatalf("AppendEvent second: %v", err)
	}
	if firstEvent.Sequence != 1 || secondEvent.Sequence != 2 {
		t.Fatalf("event sequences=(%d,%d), want (1,2)", firstEvent.Sequence, secondEvent.Sequence)
	}

	events, err := svc.ListEvents(ctx, "default", run.ID, 1, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("ListEvents after=1 got %+v", events)
	}

	if _, err := svc.UpdateRun(ctx, "default", run.ID, UpdateRunRequest{Status: StatusRunning}); err != nil {
		t.Fatalf("UpdateRun running: %v", err)
	}
	if _, err := svc.UpdateAttempt(ctx, "default", attempt.ID, UpdateAttemptRequest{Status: AttemptStatusRunning}); err != nil {
		t.Fatalf("UpdateAttempt running: %v", err)
	}
	if _, err := svc.UpdateAttempt(ctx, "default", attempt.ID, UpdateAttemptRequest{Status: AttemptStatusSucceeded}); err != nil {
		t.Fatalf("UpdateAttempt succeeded: %v", err)
	}
	completed, err := svc.UpdateRun(ctx, "default", run.ID, UpdateRunRequest{Status: StatusSucceeded})
	if err != nil {
		t.Fatalf("UpdateRun succeeded: %v", err)
	}
	if completed.Status != StatusSucceeded || completed.FinishedAt == nil {
		t.Fatalf("run should be terminal: %+v", completed)
	}
}

func TestRunIdempotencyIsScopedByTenant(t *testing.T) {
	svc := NewService(openRunTestDB(t))
	ctx := context.Background()

	first, err := svc.CreateRun(ctx, CreateRunRequest{TenantID: "tenant-a", IdempotencyKey: "same-request"})
	if err != nil {
		t.Fatalf("CreateRun tenant-a: %v", err)
	}
	second, err := svc.CreateRun(ctx, CreateRunRequest{TenantID: "tenant-b", IdempotencyKey: "same-request"})
	if err != nil {
		t.Fatalf("CreateRun tenant-b: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("cross-tenant idempotency returned the same run %q", first.ID)
	}
}

func TestIdenticalSnapshotDigestCanBeUsedByMultipleRuns(t *testing.T) {
	svc := NewService(openRunTestDB(t))
	ctx := context.Background()
	const snapshotJSON = `{"agent":{"id":"agent-1","revision":"12"},"tools":[]}`

	var snapshots []*ExecutionSnapshot
	for _, runKey := range []string{"run-one", "run-two"} {
		run, err := svc.CreateRun(ctx, CreateRunRequest{TenantID: "default", IdempotencyKey: runKey})
		if err != nil {
			t.Fatalf("CreateRun %s: %v", runKey, err)
		}
		snapshot, err := svc.CreateExecutionSnapshot(ctx, CreateExecutionSnapshotRequest{
			TenantID:         "default",
			RunID:            run.ID,
			SourceSpecDigest: "sha256:source",
			SnapshotJSON:     snapshotJSON,
		})
		if err != nil {
			t.Fatalf("CreateExecutionSnapshot %s: %v", runKey, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if snapshots[0].ID == snapshots[1].ID {
		t.Fatalf("different runs shared snapshot row %q", snapshots[0].ID)
	}
	if snapshots[0].SnapshotDigest != snapshots[1].SnapshotDigest {
		t.Fatalf("identical snapshots produced different digests: %q != %q", snapshots[0].SnapshotDigest, snapshots[1].SnapshotDigest)
	}
}

func TestExecutionSnapshotCanonicalDigest(t *testing.T) {
	first, err := CanonicalizeSnapshotJSON([]byte(`{"b":2,"a":{"y":2,"x":1}}`))
	if err != nil {
		t.Fatalf("CanonicalizeSnapshotJSON first: %v", err)
	}
	second, err := CanonicalizeSnapshotJSON([]byte(`{
		"a": {"x": 1, "y": 2},
		"b": 2
	}`))
	if err != nil {
		t.Fatalf("CanonicalizeSnapshotJSON second: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical JSON differs:\n%s\n%s", first, second)
	}
	if SnapshotDigest(first) != SnapshotDigest(second) {
		t.Fatal("equivalent snapshots produced different digests")
	}
}

func TestExecutionSnapshotRejectsCredentialValues(t *testing.T) {
	_, err := CanonicalizeSnapshotJSON([]byte(`{"provider":{"api_key":"plaintext"}}`))
	if err == nil {
		t.Fatal("CanonicalizeSnapshotJSON accepted api_key")
	}
	if !strings.Contains(err.Error(), "forbidden credential field") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := CanonicalizeSnapshotJSON([]byte(`{"provider":{"credentialRef":"secret://provider/user"}}`)); err != nil {
		t.Fatalf("credentialRef must be allowed: %v", err)
	}
}

func TestRunStateMachineRejectsInvalidTransition(t *testing.T) {
	db := openRunTestDB(t)
	svc := NewService(db)
	run, err := svc.CreateRun(context.Background(), CreateRunRequest{TenantID: "default"})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := svc.UpdateRun(context.Background(), "default", run.ID, UpdateRunRequest{Status: StatusSucceeded}); err == nil {
		t.Fatal("UpdateRun accepted preparing -> succeeded")
	}
}

func openRunTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	db, err := gorm.Open(sqlite.Open(dsn))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}
