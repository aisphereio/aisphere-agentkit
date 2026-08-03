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

package approvals

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestApprovalServiceLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	svc := NewService(db)
	ctx := context.Background()
	req, err := svc.Create(ctx, CreateRequest{TenantID: "default", RunID: "run1", UserID: "admin", Kind: "environment_operation"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.ID == "" || req.Status != StatusPending {
		t.Fatalf("unexpected request: %+v", req)
	}

	decided, err := svc.Decide(ctx, "default", req.ID, DecideRequest{Status: StatusApproved, DecidedBy: "admin"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != StatusApproved || decided.DecidedAt == nil {
		t.Fatalf("request should be approved with decided_at: %+v", decided)
	}

	if _, err := svc.Decide(ctx, "default", req.ID, DecideRequest{Status: StatusRejected, DecidedBy: "admin"}); err == nil {
		t.Fatalf("Decide on non-pending request should fail")
	}
}
