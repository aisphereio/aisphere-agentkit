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
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service is the Runtime execution-fact store. It intentionally exposes only
// the current Run/Snapshot/Attempt/Event model. Legacy run_steps APIs are not
// part of the contract.
type Service interface {
	CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)
	GetRun(ctx context.Context, tenantID, id string) (*Run, error)
	ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error)
	UpdateRun(ctx context.Context, tenantID, id string, req UpdateRunRequest) (*Run, error)

	CreateExecutionSnapshot(ctx context.Context, req CreateExecutionSnapshotRequest) (*ExecutionSnapshot, error)
	GetExecutionSnapshot(ctx context.Context, tenantID, id string) (*ExecutionSnapshot, error)

	CreateAttempt(ctx context.Context, req CreateAttemptRequest) (*RunAttempt, error)
	GetAttempt(ctx context.Context, tenantID, id string) (*RunAttempt, error)
	ListAttempts(ctx context.Context, tenantID, runID string) ([]RunAttempt, error)
	UpdateAttempt(ctx context.Context, tenantID, id string, req UpdateAttemptRequest) (*RunAttempt, error)

	AppendEvent(ctx context.Context, req AppendEventRequest) (*RuntimeEvent, error)
	ListEvents(ctx context.Context, tenantID, runID string, after uint64, limit int) ([]RuntimeEvent, error)
}

type CreateRunRequest struct {
	TenantID       string
	ProjectID      string
	ConversationID string
	AppName        string
	AgentID        string
	AgentRevision  string
	UserID         string
	PrincipalID    string
	SessionID      string
	Status         string
	TriggerType    string
	IdempotencyKey string
	InputSummary   string
	ModelRef       string
	TraceID        string
	MetadataJSON   string
}

type ListRunsFilter struct {
	TenantID  string
	ProjectID string
	AgentID   string
	AppName   string
	UserID    string
	SessionID string
	Status    string
	Limit     int
}

type UpdateRunRequest struct {
	Status           string
	FailureCode      string
	ErrorMessage     string
	CurrentAttemptID string
	MetadataJSON     *string
}

type CreateExecutionSnapshotRequest struct {
	TenantID         string
	RunID            string
	SchemaVersion    string
	SourceSpecDigest string
	AgentID          string
	AgentRevision    string
	ModelRevision    string
	ResolverVersion  string
	RuntimeEngine    string
	SnapshotJSON     string
}

type CreateAttemptRequest struct {
	TenantID        string
	RunID           string
	RuntimeEngine   string
	RuntimeBuild    string
	CompilerVersion string
	WorkerInstance  string
}

type UpdateAttemptRequest struct {
	Status             string
	CompiledPlanDigest string
	SandboxLeaseID     string
	WorkerInstance     string
	FailureCode        string
	ErrorMessage       string
}

type AppendEventRequest struct {
	TenantID     string
	RunID        string
	AttemptID    string
	EventType    string
	EventVersion string
	PayloadJSON  string
	TraceID      string
}

type gormService struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) Service {
	return &gormService{db: db}
}

func (s *gormService) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	status := firstNonEmpty(req.Status, StatusPreparing)
	if !knownRunStatus(status) {
		return nil, fmt.Errorf("unsupported run status %q", status)
	}

	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey != "" {
		var existing Run
		err := s.db.WithContext(ctx).
			Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).
			First(&existing).Error
		if err == nil {
			return &existing, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	var idempotencyKeyPtr *string
	if idempotencyKey != "" {
		idempotencyKeyPtr = &idempotencyKey
	}
	run := &Run{
		TenantID:       tenantID,
		ProjectID:      strings.TrimSpace(req.ProjectID),
		ConversationID: strings.TrimSpace(req.ConversationID),
		AppName:        strings.TrimSpace(req.AppName),
		AgentID:        strings.TrimSpace(req.AgentID),
		AgentRevision:  strings.TrimSpace(req.AgentRevision),
		UserID:         strings.TrimSpace(req.UserID),
		PrincipalID:    strings.TrimSpace(req.PrincipalID),
		SessionID:      strings.TrimSpace(req.SessionID),
		Status:         status,
		TriggerType:    strings.TrimSpace(req.TriggerType),
		IdempotencyKey: idempotencyKeyPtr,
		InputSummary:   req.InputSummary,
		ModelRef:       strings.TrimSpace(req.ModelRef),
		TraceID:        strings.TrimSpace(req.TraceID),
		MetadataJSON:   req.MetadataJSON,
	}
	applyRunStatusTimes(run, status)
	if err := s.db.WithContext(ctx).Create(run).Error; err != nil {
		return nil, err
	}
	return run, nil
}

func (s *gormService) GetRun(ctx context.Context, tenantID, id string) (*Run, error) {
	var run Run
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&run).Error
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *gormService) ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error) {
	q := s.db.WithContext(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.ProjectID != "" {
		q = q.Where("project_id = ?", filter.ProjectID)
	}
	if filter.AgentID != "" {
		q = q.Where("agent_id = ?", filter.AgentID)
	}
	if filter.AppName != "" {
		q = q.Where("app_name = ?", filter.AppName)
	}
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []Run
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *gormService) UpdateRun(ctx context.Context, tenantID, id string, req UpdateRunRequest) (*Run, error) {
	return s.updateRun(ctx, tenantID, id, func(run *Run) error {
		if req.Status != "" && req.Status != run.Status {
			if err := validateRunTransition(run.Status, req.Status); err != nil {
				return err
			}
			run.Status = req.Status
			applyRunStatusTimes(run, req.Status)
		}
		if req.FailureCode != "" {
			run.FailureCode = req.FailureCode
		}
		if req.ErrorMessage != "" {
			run.ErrorMessage = req.ErrorMessage
		}
		if req.CurrentAttemptID != "" {
			run.CurrentAttemptID = req.CurrentAttemptID
		}
		if req.MetadataJSON != nil {
			run.MetadataJSON = *req.MetadataJSON
		}
		return nil
	})
}

func (s *gormService) CreateExecutionSnapshot(ctx context.Context, req CreateExecutionSnapshotRequest) (*ExecutionSnapshot, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	schemaVersion := firstNonEmpty(req.SchemaVersion, ExecutionSnapshotSchemaV1)
	if schemaVersion != ExecutionSnapshotSchemaV1 {
		return nil, fmt.Errorf("unsupported execution snapshot schema %q", schemaVersion)
	}
	if strings.TrimSpace(req.SourceSpecDigest) == "" {
		return nil, fmt.Errorf("source_spec_digest is required")
	}
	canonical, err := CanonicalizeSnapshotJSON([]byte(req.SnapshotJSON))
	if err != nil {
		return nil, err
	}
	digest := SnapshotDigest(canonical)

	var snapshot *ExecutionSnapshot
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		if err := locked(tx).
			Where("tenant_id = ? AND id = ?", req.TenantID, req.RunID).
			First(&run).Error; err != nil {
			return err
		}
		if run.SnapshotID != "" {
			var existing ExecutionSnapshot
			if err := tx.Where("tenant_id = ? AND id = ?", req.TenantID, run.SnapshotID).First(&existing).Error; err != nil {
				return err
			}
			if existing.SnapshotDigest == digest {
				snapshot = &existing
				return nil
			}
			return fmt.Errorf("run %s already has immutable execution snapshot %s", run.ID, run.SnapshotID)
		}

		created := &ExecutionSnapshot{
			TenantID:         strings.TrimSpace(req.TenantID),
			RunID:            strings.TrimSpace(req.RunID),
			SchemaVersion:    schemaVersion,
			SourceSpecDigest: strings.TrimSpace(req.SourceSpecDigest),
			SnapshotDigest:   digest,
			AgentID:          strings.TrimSpace(req.AgentID),
			AgentRevision:    strings.TrimSpace(req.AgentRevision),
			ModelRevision:    strings.TrimSpace(req.ModelRevision),
			ResolverVersion:  strings.TrimSpace(req.ResolverVersion),
			RuntimeEngine:    firstNonEmpty(req.RuntimeEngine, "adk-go"),
			SnapshotJSON:     string(canonical),
		}
		if err := tx.Create(created).Error; err != nil {
			return err
		}
		updates := map[string]any{"snapshot_id": created.ID}
		if run.AgentID == "" && created.AgentID != "" {
			updates["agent_id"] = created.AgentID
		}
		if run.AgentRevision == "" && created.AgentRevision != "" {
			updates["agent_revision"] = created.AgentRevision
		}
		if err := tx.Model(&Run{}).
			Where("tenant_id = ? AND id = ? AND snapshot_id = ''", req.TenantID, req.RunID).
			Updates(updates).Error; err != nil {
			return err
		}
		snapshot = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *gormService) GetExecutionSnapshot(ctx context.Context, tenantID, id string) (*ExecutionSnapshot, error) {
	var snapshot ExecutionSnapshot
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&snapshot).Error
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *gormService) CreateAttempt(ctx context.Context, req CreateAttemptRequest) (*RunAttempt, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	var attempt *RunAttempt
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		if err := locked(tx).
			Where("tenant_id = ? AND id = ?", req.TenantID, req.RunID).
			First(&run).Error; err != nil {
			return err
		}
		if run.SnapshotID == "" {
			return fmt.Errorf("run %s has no execution snapshot", run.ID)
		}

		var latest uint32
		if err := tx.Model(&RunAttempt{}).
			Where("tenant_id = ? AND run_id = ?", req.TenantID, req.RunID).
			Select("COALESCE(MAX(attempt_number), 0)").
			Scan(&latest).Error; err != nil {
			return err
		}
		created := &RunAttempt{
			TenantID:        strings.TrimSpace(req.TenantID),
			RunID:           strings.TrimSpace(req.RunID),
			AttemptNumber:   latest + 1,
			Status:          AttemptStatusQueued,
			RuntimeEngine:   firstNonEmpty(req.RuntimeEngine, "adk-go"),
			RuntimeBuild:    strings.TrimSpace(req.RuntimeBuild),
			CompilerVersion: strings.TrimSpace(req.CompilerVersion),
			WorkerInstance:  strings.TrimSpace(req.WorkerInstance),
		}
		if err := tx.Create(created).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		updates := map[string]any{
			"current_attempt_id": created.ID,
			"status":             StatusQueued,
			"queued_at":          &now,
			"finished_at":        nil,
		}
		if err := tx.Model(&Run{}).
			Where("tenant_id = ? AND id = ?", req.TenantID, req.RunID).
			Updates(updates).Error; err != nil {
			return err
		}
		attempt = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attempt, nil
}

func (s *gormService) GetAttempt(ctx context.Context, tenantID, id string) (*RunAttempt, error) {
	var attempt RunAttempt
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&attempt).Error
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *gormService) ListAttempts(ctx context.Context, tenantID, runID string) ([]RunAttempt, error) {
	var out []RunAttempt
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("attempt_number ASC").
		Find(&out).Error
	return out, err
}

func (s *gormService) UpdateAttempt(ctx context.Context, tenantID, id string, req UpdateAttemptRequest) (*RunAttempt, error) {
	var attempt RunAttempt
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&attempt).Error; err != nil {
		return nil, err
	}
	if req.Status != "" && req.Status != attempt.Status {
		if err := validateAttemptTransition(attempt.Status, req.Status); err != nil {
			return nil, err
		}
		attempt.Status = req.Status
		applyAttemptStatusTimes(&attempt, req.Status)
	}
	if req.CompiledPlanDigest != "" {
		attempt.CompiledPlanDigest = strings.TrimSpace(req.CompiledPlanDigest)
	}
	if req.SandboxLeaseID != "" {
		attempt.SandboxLeaseID = strings.TrimSpace(req.SandboxLeaseID)
	}
	if req.WorkerInstance != "" {
		attempt.WorkerInstance = strings.TrimSpace(req.WorkerInstance)
	}
	if req.FailureCode != "" {
		attempt.FailureCode = strings.TrimSpace(req.FailureCode)
	}
	if req.ErrorMessage != "" {
		attempt.ErrorMessage = req.ErrorMessage
	}
	if err := s.db.WithContext(ctx).Save(&attempt).Error; err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *gormService) AppendEvent(ctx context.Context, req AppendEventRequest) (*RuntimeEvent, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	if strings.TrimSpace(req.EventType) == "" {
		return nil, fmt.Errorf("event_type is required")
	}

	var event *RuntimeEvent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		if err := locked(tx).
			Where("tenant_id = ? AND id = ?", req.TenantID, req.RunID).
			First(&run).Error; err != nil {
			return err
		}
		var latest uint64
		if err := tx.Model(&RuntimeEvent{}).
			Where("tenant_id = ? AND run_id = ?", req.TenantID, req.RunID).
			Select("COALESCE(MAX(sequence), 0)").
			Scan(&latest).Error; err != nil {
			return err
		}
		created := &RuntimeEvent{
			TenantID:     strings.TrimSpace(req.TenantID),
			RunID:        strings.TrimSpace(req.RunID),
			AttemptID:    strings.TrimSpace(req.AttemptID),
			Sequence:     latest + 1,
			EventType:    strings.TrimSpace(req.EventType),
			EventVersion: firstNonEmpty(req.EventVersion, RuntimeEventVersionV1),
			PayloadJSON:  req.PayloadJSON,
			TraceID:      strings.TrimSpace(req.TraceID),
		}
		if err := tx.Create(created).Error; err != nil {
			return err
		}
		event = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return event, nil
}

func (s *gormService) ListEvents(ctx context.Context, tenantID, runID string, after uint64, limit int) ([]RuntimeEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var out []RuntimeEvent
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ? AND sequence > ?", tenantID, runID, after).
		Order("sequence ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}

func (s *gormService) updateRun(ctx context.Context, tenantID, id string, fn func(*Run) error) (*Run, error) {
	var run Run
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&run).Error
	if err != nil {
		return nil, err
	}
	if err := fn(&run); err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Save(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func locked(tx *gorm.DB) *gorm.DB {
	switch tx.Dialector.Name() {
	case "postgres", "mysql":
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return tx
	}
}

func validateRunTransition(from, to string) error {
	if !knownRunStatus(to) {
		return fmt.Errorf("unsupported run status %q", to)
	}
	if from == to {
		return nil
	}
	allowed := map[string]map[string]bool{
		StatusPreparing: {
			StatusQueued: true, StatusRunning: true, StatusFailed: true, StatusCancelled: true,
		},
		StatusQueued: {
			StatusRunning: true, StatusFailed: true, StatusCancelled: true,
		},
		StatusRunning: {
			StatusWaitingApproval: true, StatusSucceeded: true, StatusCompleted: true, StatusFailed: true, StatusCancelled: true,
		},
		StatusWaitingApproval: {
			StatusRunning: true, StatusFailed: true, StatusCancelled: true,
		},
	}
	if allowed[from][to] {
		return nil
	}
	return fmt.Errorf("invalid run status transition %q -> %q", from, to)
}

func validateAttemptTransition(from, to string) error {
	if !knownAttemptStatus(to) {
		return fmt.Errorf("unsupported attempt status %q", to)
	}
	if from == to {
		return nil
	}
	allowed := map[string]map[string]bool{
		AttemptStatusQueued: {
			AttemptStatusRunning: true, AttemptStatusFailed: true, AttemptStatusCancelled: true,
		},
		AttemptStatusRunning: {
			AttemptStatusWaitingApproval: true, AttemptStatusSucceeded: true, AttemptStatusFailed: true, AttemptStatusCancelled: true,
		},
		AttemptStatusWaitingApproval: {
			AttemptStatusRunning: true, AttemptStatusFailed: true, AttemptStatusCancelled: true,
		},
	}
	if allowed[from][to] {
		return nil
	}
	return fmt.Errorf("invalid attempt status transition %q -> %q", from, to)
}

func knownRunStatus(status string) bool {
	switch status {
	case StatusPreparing, StatusQueued, StatusRunning, StatusWaitingApproval, StatusSucceeded, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func knownAttemptStatus(status string) bool {
	switch status {
	case AttemptStatusQueued, AttemptStatusRunning, AttemptStatusWaitingApproval, AttemptStatusSucceeded, AttemptStatusFailed, AttemptStatusCancelled:
		return true
	default:
		return false
	}
}

func isTerminalRunStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func isTerminalAttemptStatus(status string) bool {
	switch status {
	case AttemptStatusSucceeded, AttemptStatusFailed, AttemptStatusCancelled:
		return true
	default:
		return false
	}
}

func applyRunStatusTimes(run *Run, status string) {
	now := time.Now().UTC()
	switch status {
	case StatusQueued:
		run.QueuedAt = &now
		run.FinishedAt = nil
	case StatusRunning:
		if run.StartedAt.IsZero() {
			run.StartedAt = now
		}
		run.FinishedAt = nil
	case StatusWaitingApproval:
		run.FinishedAt = nil
	case StatusCancelled:
		run.CancelledAt = &now
		run.FinishedAt = &now
	case StatusSucceeded, StatusCompleted, StatusFailed:
		run.FinishedAt = &now
	}
}

func applyAttemptStatusTimes(attempt *RunAttempt, status string) {
	now := time.Now().UTC()
	switch status {
	case AttemptStatusRunning:
		if attempt.StartedAt == nil {
			attempt.StartedAt = &now
		}
		attempt.FinishedAt = nil
	case AttemptStatusWaitingApproval:
		attempt.FinishedAt = nil
	default:
		if isTerminalAttemptStatus(status) {
			attempt.FinishedAt = &now
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
