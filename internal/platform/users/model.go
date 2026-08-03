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
	"time"

	"gorm.io/gorm"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusArchived = "archived"
)

// Tenant is the platform isolation boundary. P0/P1 deployments can use the
// default tenant; the table exists now so later user/session/project records
// have a stable owner.
type Tenant struct {
	ID           string    `json:"id" gorm:"primaryKey;size:128"`
	Name         string    `json:"name" gorm:"size:256;not null"`
	Status       string    `json:"status" gorm:"index;size:64;not null"`
	Description  string    `json:"description,omitempty" gorm:"type:text"`
	MetadataJSON string    `json:"metadata_json,omitempty" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"precision:6"`
}

func (t *Tenant) BeforeCreate(tx *gorm.DB) error {
	if t.Status == "" {
		t.Status = StatusActive
	}
	if t.Name == "" {
		t.Name = t.ID
	}
	return nil
}

// User is the platform account record. Authentication is still dev-token based
// in P1, but this table becomes the durable owner for sessions, runs, projects,
// skills, model credentials, and environment assets.
type User struct {
	ID           string    `json:"id" gorm:"primaryKey;size:256"`
	TenantID     string    `json:"tenant_id" gorm:"primaryKey;size:128;index"`
	Username     string    `json:"username,omitempty" gorm:"size:256;index"`
	Email        string    `json:"email,omitempty" gorm:"size:320;index"`
	DisplayName  string    `json:"display_name,omitempty" gorm:"size:256"`
	PasswordHash string    `json:"-" gorm:"type:text"`
	Status       string    `json:"status" gorm:"index;size:64;not null"`
	MetadataJSON string    `json:"metadata_json,omitempty" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"precision:6"`
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.Status == "" {
		u.Status = StatusActive
	}
	if u.Username == "" {
		u.Username = u.ID
	}
	if u.DisplayName == "" {
		u.DisplayName = u.Username
	}
	return nil
}

// Role is a tenant-scoped RBAC label. Enforcement is still coarse-grained in
// P1; this table stores the durable assignment model for later middleware.
type Role struct {
	ID          string    `json:"id" gorm:"primaryKey;size:128"`
	TenantID    string    `json:"tenant_id" gorm:"primaryKey;size:128;index"`
	Name        string    `json:"name" gorm:"size:128;index;not null"`
	Description string    `json:"description,omitempty" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"precision:6"`
}

// UserRole assigns a role to a user inside a tenant.
type UserRole struct {
	TenantID  string    `json:"tenant_id" gorm:"primaryKey;size:128;index"`
	UserID    string    `json:"user_id" gorm:"primaryKey;size:256;index"`
	RoleID    string    `json:"role_id" gorm:"primaryKey;size:128;index"`
	CreatedAt time.Time `json:"created_at" gorm:"precision:6"`
}

// AutoMigrate creates or updates platform user/tenant tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Tenant{}, &User{}, &Role{}, &UserRole{})
}
