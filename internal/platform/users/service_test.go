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

package users

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUserServiceBootstrapAndLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	svc := NewService(db)
	ctx := context.Background()
	if err := svc.BootstrapPrincipal(ctx, "default", "admin", []string{"owner"}); err != nil {
		t.Fatalf("BootstrapPrincipal: %v", err)
	}
	if _, err := svc.GetTenant(ctx, "default"); err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	user, err := svc.GetUser(ctx, "default", "admin")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Status != StatusActive {
		t.Fatalf("user status = %q, want active", user.Status)
	}
	created, err := svc.CreateUser(ctx, CreateUserRequest{TenantID: "default", ID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.Username != "u1" {
		t.Fatalf("username = %q, want u1", created.Username)
	}
}
