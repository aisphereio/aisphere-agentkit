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
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusExpired  = "expired"
)

// Request stores a human-in-the-loop decision needed by a run. Examples:
// tool confirmation, environment operation approval, user choice, or user form.
type Request struct {
	ID          string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID    string     `json:"tenant_id" gorm:"index;size:128;not null"`
	RunID       string     `json:"run_id,omitempty" gorm:"index;size:64"`
	SessionID   string     `json:"session_id,omitempty" gorm:"index;size:256"`
	UserID      string     `json:"user_id,omitempty" gorm:"index;size:256"`
	Kind        string     `json:"kind" gorm:"index;size:128;not null"`
	Status      string     `json:"status" gorm:"index;size:64;not null"`
	PayloadJSON string     `json:"payload_json,omitempty" gorm:"type:text"`
	Reason      string     `json:"reason,omitempty" gorm:"type:text"`
	DecidedBy   string     `json:"decided_by,omitempty" gorm:"size:256"`
	DecidedAt   *time.Time `json:"decided_at,omitempty" gorm:"precision:6"`
	CreatedAt   time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"precision:6"`
}

// TableName keeps the database name explicit. The Go type is named Request for
// readability, but the platform schema uses approval_requests.
func (Request) TableName() string {
	return "approval_requests"
}

func (r *Request) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = StatusPending
	}
	return nil
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Request{})
}
