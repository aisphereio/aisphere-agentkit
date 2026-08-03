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
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusCancelled       = "cancelled"
)

// Run is the platform-level execution record. It is intentionally separate
// from ADK session events: sessions capture conversation history, while runs
// capture operational lifecycle, resumability, approvals, and audits.
type Run struct {
	ID           string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID     string     `json:"tenant_id" gorm:"index;size:128;not null"`
	AppName      string     `json:"app_name" gorm:"index;size:256"`
	UserID       string     `json:"user_id" gorm:"index;size:256"`
	SessionID    string     `json:"session_id" gorm:"index;size:256"`
	Status       string     `json:"status" gorm:"index;size:64;not null"`
	InputSummary string     `json:"input_summary,omitempty" gorm:"type:text"`
	ModelRef     string     `json:"model_ref,omitempty" gorm:"size:256"`
	ErrorMessage string     `json:"error_message,omitempty" gorm:"type:text"`
	MetadataJSON string     `json:"metadata_json,omitempty" gorm:"type:text"`
	StartedAt    time.Time  `json:"started_at" gorm:"precision:6"`
	FinishedAt   *time.Time `json:"finished_at,omitempty" gorm:"precision:6"`
	CreatedAt    time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"precision:6"`
}

func (r *Run) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = StatusRunning
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	return nil
}

// Step is a coarse-grained run timeline item used by platform UI/debugging.
type Step struct {
	ID           string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID     string     `json:"tenant_id" gorm:"index;size:128;not null"`
	RunID        string     `json:"run_id" gorm:"index;size:64;not null"`
	Kind         string     `json:"kind" gorm:"index;size:64;not null"`
	Status       string     `json:"status" gorm:"index;size:64;not null"`
	PayloadJSON  string     `json:"payload_json,omitempty" gorm:"type:text"`
	ErrorMessage string     `json:"error_message,omitempty" gorm:"type:text"`
	StartedAt    time.Time  `json:"started_at" gorm:"precision:6"`
	FinishedAt   *time.Time `json:"finished_at,omitempty" gorm:"precision:6"`
	CreatedAt    time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"precision:6"`
}

// TableName keeps run steps grouped with run lifecycle tables.
func (Step) TableName() string {
	return "run_steps"
}

func (s *Step) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.Status == "" {
		s.Status = StatusRunning
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now().UTC()
	}
	return nil
}

// AutoMigrate creates or updates the run tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Run{}, &Step{})
}
