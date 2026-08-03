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

package uploads

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	StatusActive  = "active"
	StatusDeleted = "deleted"
)

// Upload is a platform-level user upload record.
//
// Uploads are intentionally separated from artifacts:
//   - uploads are raw user inputs and are never injected into model context by default;
//   - artifacts are agent/session outputs or curated files used by tools;
//   - an upload can be explicitly attached to an artifact workspace when an agent needs it.
type Upload struct {
	ID           string `json:"id" gorm:"primaryKey;size:64"`
	TenantID     string `json:"tenant_id" gorm:"index;size:128;not null"`
	UserID       string `json:"user_id" gorm:"index;size:256;not null"`
	ProjectID    string `json:"project_id,omitempty" gorm:"index;size:64"`
	AppName      string `json:"app_name,omitempty" gorm:"index;size:256"`
	SessionID    string `json:"session_id,omitempty" gorm:"index;size:256"`
	Purpose      string `json:"purpose,omitempty" gorm:"index;size:128"`
	OriginalName string `json:"original_name" gorm:"size:512;not null"`
	StoredName   string `json:"stored_name" gorm:"size:512;not null"`
	StoragePath  string `json:"storage_path,omitempty" gorm:"size:1024;not null"`
	MIMEType     string `json:"mime_type,omitempty" gorm:"size:256"`
	SizeBytes    int64  `json:"size_bytes" gorm:"not null"`
	SHA256       string `json:"sha256" gorm:"index;size:64"`
	Status       string `json:"status" gorm:"index;size:64;not null"`
	// HandlingMode is the platform policy for this raw upload. It tells the
	// frontend and agents whether the file may be inlined, must be preprocessed,
	// should be treated as a tool workspace input, or is blocked.
	HandlingMode   string    `json:"handling_mode" gorm:"index;size:64"`
	InlineEligible bool      `json:"inline_eligible" gorm:"not null;default:false"`
	Previewable    bool      `json:"previewable" gorm:"not null;default:false"`
	PolicyReason   string    `json:"policy_reason,omitempty" gorm:"size:512"`
	MetadataJSON   string    `json:"metadata_json,omitempty" gorm:"type:text"`
	CreatedAt      time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"precision:6"`
}

func (u *Upload) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	if u.Status == "" {
		u.Status = StatusActive
	}
	if u.HandlingMode == "" {
		u.HandlingMode = HandlingReferenceOnly
	}
	return nil
}

// AutoMigrate creates or updates platform upload tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Upload{})
}
