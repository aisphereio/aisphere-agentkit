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

package projects

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestProjectServiceLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	svc := NewService(db)
	ctx := context.Background()
	project, err := svc.Create(ctx, CreateRequest{TenantID: "default", OwnerUserID: "admin", Name: "demo", AppName: "test1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if project.ID == "" || project.Status != StatusActive {
		t.Fatalf("unexpected project: %+v", project)
	}
	listed, err := svc.List(ctx, ListFilter{TenantID: "default", AppName: "test1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List len=%d, want 1", len(listed))
	}
	archived, err := svc.Archive(ctx, "default", project.ID)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Status != StatusArchived {
		t.Fatalf("status=%q, want archived", archived.Status)
	}
}

func TestProjectServiceDeleteRemovesProjectAndMembers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	svc := NewService(db)
	ctx := context.Background()

	project, err := svc.Create(ctx, CreateRequest{TenantID: "default", OwnerUserID: "admin", Name: "delete-me", AppName: "book_dissector"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(ctx, "default", project.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get(ctx, "default", project.ID); err == nil {
		t.Fatalf("Get succeeded after delete, want not found")
	}

	var memberCount int64
	if err := db.Model(&ProjectMember{}).Where("tenant_id = ? AND project_id = ?", "default", project.ID).Count(&memberCount).Error; err != nil {
		t.Fatalf("count members: %v", err)
	}
	if memberCount != 0 {
		t.Fatalf("memberCount=%d, want 0", memberCount)
	}
}
