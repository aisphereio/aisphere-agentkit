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
	StatusPreparing       = "preparing"
	StatusQueued          = "queued"
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusSucceeded       = "succeeded"
	// StatusCompleted is retained while callers migrate to StatusSucceeded.
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	AttemptStatusQueued          = "queued"
	AttemptStatusRunning         = "running"
	AttemptStatusWaitingApproval = "waiting_approval"
	AttemptStatusSucceeded       = "succeeded"
	AttemptStatusFailed          = "failed"
	AttemptStatusCancelled       = "cancelled"

	ExecutionSnapshotSchemaV1 = "execution-snapshot/v1"
	RuntimeEventVersionV1      = "runtime-event/v1"
)

// Run is one logical user-requested execution. Physical retries are represented
// by RunAttempt records and always retain the same immutable ExecutionSnapshot.
type Run struct {
	ID               string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID         string     `json:"tenant_id" gorm:"index;size:128;not null"`
	ProjectID        string     `json:"project_id,omitempty" gorm:"index;size:128"`
	ConversationID   string     `json:"conversation_id,omitempty" gorm:"index;size:128"`
	AppName          string     `json:"app_name,omitempty" gorm:"index;size:256"`
	AgentID          string     `json:"agent_id,omitempty" gorm:"index;size:128"`
	AgentRevision    string     `json:"agent_revision,omitempty" gorm:"size:128"`
	UserID           string     `json:"user_id,omitempty" gorm:"index;size:256"`
	PrincipalID      string     `json:"principal_id,omitempty" gorm:"index;size:256"`
	SessionID        string     `json:"session_id,omitempty" gorm:"index;size:256"`
	SnapshotID       string     `json:"snapshot_id,omitempty" gorm:"index;size:64"`
	CurrentAttemptID string     `json:"current_attempt_id,omitempty" gorm:"index;size:64"`
	Status           string     `json:"status" gorm:"index;size:64;not null"`
	TriggerType      string     `json:"trigger_type,omitempty" gorm:"size:64"`
	IdempotencyKey   *string    `json:"idempotency_key,omitempty" gorm:"uniqueIndex:idx_runtime_runs_tenant_idempotency,priority:2;size:256"`
	InputSummary     string     `json:"input_summary,omitempty" gorm:"type:text"`
	ModelRef         string     `json:"model_ref,omitempty" gorm:"size:256"`
	TraceID          string     `json:"trace_id,omitempty" gorm:"index;size:128"`
	FailureCode      string     `json:"failure_code,omitempty" gorm:"size:128"`
	ErrorMessage     string     `json:"error_message,omitempty" gorm:"type:text"`
	MetadataJSON     string     `json:"metadata_json,omitempty" gorm:"type:text"`
	QueuedAt         *time.Time `json:"queued_at,omitempty" gorm:"precision:6"`
	StartedAt        time.Time  `json:"started_at" gorm:"precision:6"`
	FinishedAt       *time.Time `json:"finished_at,omitempty" gorm:"precision:6"`
	CancelledAt      *time.Time `json:"cancelled_at,omitempty" gorm:"precision:6"`
	CreatedAt        time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"precision:6"`
}

func (Run) TableName() string {
	return "runtime_runs"
}

func (r *Run) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = StatusPreparing
	}
	return nil
}

// ExecutionSnapshot is the immutable per-run execution contract. SnapshotJSON
// contains canonical JSON and never contains credential values or dynamic
// sandbox endpoints. There is intentionally no UpdateSnapshot service method.
type ExecutionSnapshot struct {
	ID               string    `json:"id" gorm:"primaryKey;size:64"`
	TenantID         string    `json:"tenant_id" gorm:"index;size:128;not null"`
	RunID            string    `json:"run_id" gorm:"uniqueIndex;size:64;not null"`
	SchemaVersion    string    `json:"schema_version" gorm:"index;size:64;not null"`
	SourceSpecDigest string    `json:"source_spec_digest" gorm:"size:128;not null"`
	SnapshotDigest   string    `json:"snapshot_digest" gorm:"uniqueIndex;size:128;not null"`
	AgentID          string    `json:"agent_id,omitempty" gorm:"index;size:128"`
	AgentRevision    string    `json:"agent_revision,omitempty" gorm:"size:128"`
	ModelRevision    string    `json:"model_revision,omitempty" gorm:"size:128"`
	ResolverVersion  string    `json:"resolver_version,omitempty" gorm:"size:64"`
	RuntimeEngine    string    `json:"runtime_engine" gorm:"size:64;not null"`
	SnapshotJSON     string    `json:"snapshot_json" gorm:"type:text;not null"`
	CreatedAt        time.Time `json:"created_at" gorm:"precision:6"`
}

func (ExecutionSnapshot) TableName() string {
	return "runtime_execution_snapshots"
}

func (s *ExecutionSnapshot) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.SchemaVersion == "" {
		s.SchemaVersion = ExecutionSnapshotSchemaV1
	}
	if s.RuntimeEngine == "" {
		s.RuntimeEngine = "adk-go"
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	return nil
}

// RunAttempt is one physical execution attempt for a Run. A retry creates a new
// attempt and reuses the Run's immutable ExecutionSnapshot.
type RunAttempt struct {
	ID                 string     `json:"id" gorm:"primaryKey;size:64"`
	TenantID           string     `json:"tenant_id" gorm:"index;size:128;not null"`
	RunID              string     `json:"run_id" gorm:"uniqueIndex:idx_runtime_attempt_number,priority:1;index;size:64;not null"`
	AttemptNumber      uint32     `json:"attempt_number" gorm:"uniqueIndex:idx_runtime_attempt_number,priority:2;not null"`
	Status             string     `json:"status" gorm:"index;size:64;not null"`
	RuntimeEngine      string     `json:"runtime_engine" gorm:"size:64;not null"`
	RuntimeBuild       string     `json:"runtime_build,omitempty" gorm:"size:128"`
	CompilerVersion    string     `json:"compiler_version,omitempty" gorm:"size:64"`
	CompiledPlanDigest string     `json:"compiled_plan_digest,omitempty" gorm:"size:128"`
	SandboxLeaseID     string     `json:"sandbox_lease_id,omitempty" gorm:"index;size:128"`
	WorkerInstance     string     `json:"worker_instance,omitempty" gorm:"size:256"`
	FailureCode        string     `json:"failure_code,omitempty" gorm:"size:128"`
	ErrorMessage       string     `json:"error_message,omitempty" gorm:"type:text"`
	StartedAt          *time.Time `json:"started_at,omitempty" gorm:"precision:6"`
	FinishedAt         *time.Time `json:"finished_at,omitempty" gorm:"precision:6"`
	CreatedAt          time.Time  `json:"created_at" gorm:"precision:6"`
	UpdatedAt          time.Time  `json:"updated_at" gorm:"precision:6"`
}

func (RunAttempt) TableName() string {
	return "runtime_run_attempts"
}

func (a *RunAttempt) BeforeCreate(tx *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Status == "" {
		a.Status = AttemptStatusQueued
	}
	if a.RuntimeEngine == "" {
		a.RuntimeEngine = "adk-go"
	}
	return nil
}

// RuntimeEvent is an append-only fact. Sequence is strictly increasing per Run
// and is used as the resumable SSE cursor.
type RuntimeEvent struct {
	ID          string    `json:"id" gorm:"primaryKey;size:64"`
	TenantID    string    `json:"tenant_id" gorm:"index;size:128;not null"`
	RunID       string    `json:"run_id" gorm:"uniqueIndex:idx_runtime_event_sequence,priority:1;index;size:64;not null"`
	AttemptID   string    `json:"attempt_id,omitempty" gorm:"index;size:64"`
	Sequence    uint64    `json:"sequence" gorm:"uniqueIndex:idx_runtime_event_sequence,priority:2;not null"`
	EventType   string    `json:"event_type" gorm:"index;size:128;not null"`
	EventVersion string   `json:"event_version" gorm:"size:64;not null"`
	PayloadJSON string    `json:"payload_json,omitempty" gorm:"type:text"`
	TraceID     string    `json:"trace_id,omitempty" gorm:"index;size:128"`
	CreatedAt   time.Time `json:"created_at" gorm:"precision:6"`
}

func (RuntimeEvent) TableName() string {
	return "runtime_events"
}

func (e *RuntimeEvent) BeforeCreate(tx *gorm.DB) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.EventVersion == "" {
		e.EventVersion = RuntimeEventVersionV1
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	return nil
}

// Step is the legacy coarse-grained timeline model. New execution code should
// append RuntimeEvent records instead. It remains during API migration only.
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

// AutoMigrate creates the Runtime fact tables and the temporary legacy step
// table. Production migrations will replace AutoMigrate before launch.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Run{},
		&ExecutionSnapshot{},
		&RunAttempt{},
		&RuntimeEvent{},
		&Step{},
	)
}
