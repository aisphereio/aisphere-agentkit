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

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRunServiceLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	svc := NewService(db)
	ctx := context.Background()
	run, err := svc.CreateRun(ctx, CreateRunRequest{TenantID: "default", AppName: "test1", UserID: "admin", SessionID: "s1"})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.ID == "" || run.Status != StatusRunning {
		t.Fatalf("unexpected run: %+v", run)
	}

	step, err := svc.CreateStep(ctx, CreateStepRequest{TenantID: "default", RunID: run.ID, Kind: "llm"})
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	if step.ID == "" || step.Status != StatusRunning {
		t.Fatalf("unexpected step: %+v", step)
	}

	updated, err := svc.UpdateRun(ctx, "default", run.ID, UpdateRunRequest{Status: StatusCompleted})
	if err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	if updated.Status != StatusCompleted || updated.FinishedAt == nil {
		t.Fatalf("run should be terminal with finished_at: %+v", updated)
	}

	listed, err := svc.ListRuns(ctx, ListRunsFilter{TenantID: "default", AppName: "test1"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListRuns len=%d, want 1", len(listed))
	}
}
