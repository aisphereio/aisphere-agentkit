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

// Package novelstore manages long-lived novel/book assets under a project.
// Metadata lives in the platform database; large text and generated files live
// in ObjectStore/MinIO using project-first object keys.
package novelstore

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	StatusActive     = "active"
	StatusArchived   = "archived"
	StatusDeleted    = "deleted"
	StatusSuperseded = "superseded"
	StatusFailed     = "failed"

	ArtifactChapterAnalysis = "chapter_analysis"
	ArtifactSkillPackage    = "chapter_skill_pack"
	ArtifactGapReport       = "gap_report"
	ArtifactBatchSummary    = "batch_summary"
	ArtifactExportPackage   = "export_package"
)

// Book is a logical novel source imported into a project.
type Book struct {
	ID              string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID        string     `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID       string     `json:"project_id" gorm:"index;size:64;not null"`
	OwnerUserID     string     `json:"owner_user_id" gorm:"index;size:256"`
	Title           string     `json:"title" gorm:"size:512;not null"`
	Author          string     `json:"author,omitempty" gorm:"size:256"`
	SourceUploadID  string     `json:"source_upload_id,omitempty" gorm:"index;size:64"`
	SourceObjectKey string     `json:"source_object_key" gorm:"size:1024;not null"`
	CurrentSplitID  string     `json:"current_split_id,omitempty" gorm:"index;size:64"`
	ChapterCount    int        `json:"chapter_count" gorm:"not null;default:0"`
	TotalChars      int        `json:"total_chars" gorm:"not null;default:0"`
	SizeBytes       int64      `json:"size_bytes" gorm:"not null;default:0"`
	SHA256          string     `json:"sha256,omitempty" gorm:"index;size:64"`
	Encoding        string     `json:"encoding,omitempty" gorm:"size:64"`
	Status          string     `json:"status" gorm:"index;size:64;not null"`
	MetadataJSON    string     `json:"metadata_json,omitempty" gorm:"type:text"`
	CreatedAt       time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"precision:6"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty" gorm:"index;precision:6"`
}

func (b *Book) BeforeCreate(tx *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.Status == "" {
		b.Status = StatusActive
	}
	return nil
}

// Split is one concrete chapter split version for a book.
type Split struct {
	ID                string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID          string     `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID         string     `json:"project_id" gorm:"index;size:64;not null"`
	BookID            string     `json:"book_id" gorm:"index;size:64;not null"`
	SourceObjectKey   string     `json:"source_object_key" gorm:"size:1024;not null"`
	ManifestObjectKey string     `json:"manifest_object_key" gorm:"size:1024;not null"`
	SplitMethod       string     `json:"split_method" gorm:"size:128"`
	ChapterCount      int        `json:"chapter_count" gorm:"not null;default:0"`
	TotalChars        int        `json:"total_chars" gorm:"not null;default:0"`
	TotalBytes        int64      `json:"total_bytes" gorm:"not null;default:0"`
	WarningsJSON      string     `json:"warnings_json,omitempty" gorm:"type:text"`
	Status            string     `json:"status" gorm:"index;size:64;not null"`
	CreatedAt         time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt         time.Time  `json:"updated_at" gorm:"precision:6"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty" gorm:"index;precision:6"`
}

func (s *Split) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.Status == "" {
		s.Status = StatusActive
	}
	return nil
}

// Chapter is a single chapter text object under a split.
type Chapter struct {
	ID        string `json:"id" gorm:"primaryKey;size:64"`
	TenantID  string `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID string `json:"project_id" gorm:"index;size:64;not null"`
	BookID    string `json:"book_id" gorm:"index;size:64;not null"`
	SplitID   string `json:"split_id" gorm:"index;size:64;not null"`

	ChapterNo int    `json:"chapter_no" gorm:"index;not null"`
	Title     string `json:"title" gorm:"size:512;not null"`
	ObjectKey string `json:"object_key" gorm:"size:1024;not null"`
	CharCount int    `json:"char_count" gorm:"not null;default:0"`
	ByteCount int64  `json:"byte_count" gorm:"not null;default:0"`
	SHA256    string `json:"sha256,omitempty" gorm:"index;size:64"`
	StartChar int    `json:"start_char" gorm:"not null;default:0"`
	EndChar   int    `json:"end_char" gorm:"not null;default:0"`
	Status    string `json:"status" gorm:"index;size:64;not null"`

	CreatedAt time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"precision:6"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index;precision:6"`
}

func (c *Chapter) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = StatusActive
	}
	return nil
}

// Artifact stores metadata for novel-domain generated files.
type Artifact struct {
	ID        string `json:"id" gorm:"primaryKey;size:64"`
	TenantID  string `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID string `json:"project_id" gorm:"index;size:64;not null"`
	BookID    string `json:"book_id" gorm:"index;size:64;not null"`
	SplitID   string `json:"split_id,omitempty" gorm:"index;size:64"`
	ChapterID string `json:"chapter_id,omitempty" gorm:"index;size:64"`
	RunID     string `json:"run_id,omitempty" gorm:"index;size:128"`

	Kind         string     `json:"kind" gorm:"index;size:128;not null"`
	Name         string     `json:"name" gorm:"size:512;not null"`
	Title        string     `json:"title,omitempty" gorm:"size:512"`
	ObjectKey    string     `json:"object_key" gorm:"size:1024;not null"`
	MIMEType     string     `json:"mime_type,omitempty" gorm:"size:256"`
	SizeBytes    int64      `json:"size_bytes" gorm:"not null;default:0"`
	SHA256       string     `json:"sha256,omitempty" gorm:"index;size:64"`
	MetadataJSON string     `json:"metadata_json,omitempty" gorm:"type:text"`
	Status       string     `json:"status" gorm:"index;size:64;not null"`
	CreatedAt    time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"precision:6"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" gorm:"index;precision:6"`
}

func (a *Artifact) BeforeCreate(tx *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Status == "" {
		a.Status = StatusActive
	}
	return nil
}

// AutoMigrate creates or updates novelstore metadata tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Book{}, &Split{}, &Chapter{}, &Artifact{})
}
