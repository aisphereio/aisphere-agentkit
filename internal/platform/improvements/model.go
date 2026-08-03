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

package improvements

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	IssueStatusOpen      = "open"
	IssueStatusProposed  = "proposed"
	IssueStatusDismissed = "dismissed"
	IssueStatusResolved  = "resolved"

	ProposalStatusDraft         = "draft"
	ProposalStatusPendingReview = "pending_review"
	ProposalStatusApproved      = "approved"
	ProposalStatusRejected      = "rejected"
	ProposalStatusApplied       = "applied"
	ProposalStatusFailed        = "failed"

	ChangeStatusDraft     = "draft"
	ChangeStatusApproved  = "approved"
	ChangeStatusApplied   = "applied"
	ChangeStatusSupersede = "superseded"
)

// Issue records a concrete improvement opportunity discovered from a real run.
// Business agents may raise issues, but they do not directly mutate agents,
// skills, tools, or workflows.
type Issue struct {
	ID           string    `json:"id" gorm:"primaryKey;size:64"`
	TenantID     string    `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID    string    `json:"project_id,omitempty" gorm:"index;size:64"`
	RunID        string    `json:"run_id,omitempty" gorm:"index;size:64"`
	SessionID    string    `json:"session_id,omitempty" gorm:"index;size:256"`
	AppName      string    `json:"app_name,omitempty" gorm:"index;size:256"`
	AgentName    string    `json:"agent_name,omitempty" gorm:"index;size:256"`
	IssueType    string    `json:"issue_type" gorm:"index;size:128;not null"`
	Severity     string    `json:"severity" gorm:"index;size:64;not null"`
	Title        string    `json:"title" gorm:"type:text;not null"`
	Description  string    `json:"description,omitempty" gorm:"type:text"`
	EvidenceJSON string    `json:"evidence_json,omitempty" gorm:"type:text"`
	Status       string    `json:"status" gorm:"index;size:64;not null"`
	CreatedBy    string    `json:"created_by,omitempty" gorm:"size:256"`
	CreatedAt    time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"precision:6"`
}

func (Issue) TableName() string {
	return "improvement_issues"
}

func (i *Issue) BeforeCreate(tx *gorm.DB) error {
	if i.ID == "" {
		i.ID = uuid.NewString()
	}
	if i.Status == "" {
		i.Status = IssueStatusOpen
	}
	return nil
}

// Proposal is a human-reviewable plan for changing platform assets such as
// agent YAML, skills, tool schemas, workflow definitions, or docs.
type Proposal struct {
	ID              string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID        string     `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID       string     `json:"project_id,omitempty" gorm:"index;size:64"`
	SourceIssueID   string     `json:"source_issue_id,omitempty" gorm:"index;size:64"`
	RunID           string     `json:"run_id,omitempty" gorm:"index;size:64"`
	SessionID       string     `json:"session_id,omitempty" gorm:"index;size:256"`
	AppName         string     `json:"app_name,omitempty" gorm:"index;size:256"`
	Title           string     `json:"title" gorm:"type:text;not null"`
	Summary         string     `json:"summary,omitempty" gorm:"type:text"`
	ProposalType    string     `json:"proposal_type" gorm:"index;size:128;not null"`
	TargetRefsJSON  string     `json:"target_refs_json,omitempty" gorm:"type:text"`
	EvidenceJSON    string     `json:"evidence_json,omitempty" gorm:"type:text"`
	RiskLevel       string     `json:"risk_level" gorm:"index;size:64;not null"`
	Status          string     `json:"status" gorm:"index;size:64;not null"`
	ApprovalID      string     `json:"approval_id,omitempty" gorm:"index;size:64"`
	CreatedByAgent  string     `json:"created_by_agent,omitempty" gorm:"size:256"`
	ReviewedBy      string     `json:"reviewed_by,omitempty" gorm:"size:256"`
	ReviewReason    string     `json:"review_reason,omitempty" gorm:"type:text"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty" gorm:"precision:6"`
	AppliedBy       string     `json:"applied_by,omitempty" gorm:"size:256"`
	AppliedAt       *time.Time `json:"applied_at,omitempty" gorm:"precision:6"`
	ApplyResultJSON string     `json:"apply_result_json,omitempty" gorm:"type:text"`
	CreatedAt       time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"precision:6"`
}

func (Proposal) TableName() string {
	return "improvement_proposals"
}

func (p *Proposal) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Status == "" {
		p.Status = ProposalStatusPendingReview
	}
	return nil
}

// Change stores the concrete diff/patch draft that a proposal asks a human to
// approve. The platform can validate and display these before anything applies.
type Change struct {
	ID                     string    `json:"id" gorm:"primaryKey;size:64"`
	TenantID               string    `json:"tenant_id" gorm:"index;size:128;not null"`
	ProposalID             string    `json:"proposal_id" gorm:"index;size:64;not null"`
	ChangeType             string    `json:"change_type" gorm:"index;size:128;not null"`
	TargetPath             string    `json:"target_path,omitempty" gorm:"index;size:512"`
	BeforeContentObjectKey string    `json:"before_content_object_key,omitempty" gorm:"size:512"`
	AfterContentObjectKey  string    `json:"after_content_object_key,omitempty" gorm:"size:512"`
	DiffText               string    `json:"diff_text,omitempty" gorm:"type:text"`
	PatchText              string    `json:"patch_text,omitempty" gorm:"type:text"`
	Status                 string    `json:"status" gorm:"index;size:64;not null"`
	CreatedAt              time.Time `json:"created_at" gorm:"precision:6"`
	UpdatedAt              time.Time `json:"updated_at" gorm:"precision:6"`
}

func (Change) TableName() string {
	return "improvement_changes"
}

func (c *Change) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = ChangeStatusDraft
	}
	return nil
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Issue{}, &Proposal{}, &Change{})
}
