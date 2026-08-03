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
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"
	StatusDisabled = "disabled"
)

// Project is the durable product/workbench grouping above sessions. It can map
// to an app/agent name, but it is not an ADK runtime object itself.
type Project struct {
	ID           string    `json:"id" gorm:"primaryKey;size:64"`
	TenantID     string    `json:"tenant_id" gorm:"index;size:128;not null"`
	OwnerUserID  string    `json:"owner_user_id" gorm:"index;size:256"`
	Name         string    `json:"name" gorm:"index;size:256;not null"`
	DisplayName  string    `json:"display_name,omitempty" gorm:"size:256"`
	Description  string    `json:"description,omitempty" gorm:"type:text"`
	AppName      string    `json:"app_name,omitempty" gorm:"index;size:256"`
	Status       string    `json:"status" gorm:"index;size:64;not null"`
	MetadataJSON string    `json:"metadata_json,omitempty" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"precision:6"`
}

func (p *Project) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Status == "" {
		p.Status = StatusActive
	}
	if p.DisplayName == "" {
		p.DisplayName = p.Name
	}
	return nil
}

// ProjectMember tracks project-level collaboration and future ACLs.
type ProjectMember struct {
	TenantID  string    `json:"tenant_id" gorm:"primaryKey;size:128;index"`
	ProjectID string    `json:"project_id" gorm:"primaryKey;size:64;index"`
	UserID    string    `json:"user_id" gorm:"primaryKey;size:256;index"`
	Role      string    `json:"role" gorm:"size:64;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt time.Time `json:"updated_at" gorm:"precision:6"`
}

// AutoMigrate creates or updates platform project tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Project{}, &ProjectMember{})
}
