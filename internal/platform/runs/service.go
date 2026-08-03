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
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Service manages platform run lifecycle records.
type Service interface {
	CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)
	GetRun(ctx context.Context, tenantID, id string) (*Run, error)
	ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error)
	UpdateRun(ctx context.Context, tenantID, id string, req UpdateRunRequest) (*Run, error)
	CreateStep(ctx context.Context, req CreateStepRequest) (*Step, error)
	ListSteps(ctx context.Context, tenantID, runID string) ([]Step, error)
	UpdateStep(ctx context.Context, tenantID, id string, req UpdateStepRequest) (*Step, error)
}

type CreateRunRequest struct {
	TenantID     string
	AppName      string
	UserID       string
	SessionID    string
	Status       string
	InputSummary string
	ModelRef     string
	MetadataJSON string
}

type ListRunsFilter struct {
	TenantID  string
	AppName   string
	UserID    string
	SessionID string
	Status    string
	Limit     int
}

type UpdateRunRequest struct {
	Status       string
	ErrorMessage string
	MetadataJSON *string
}

type CreateStepRequest struct {
	TenantID    string
	RunID       string
	Kind        string
	Status      string
	PayloadJSON string
}

type UpdateStepRequest struct {
	Status       string
	ErrorMessage string
	PayloadJSON  *string
}

type gormService struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) Service {
	return &gormService{db: db}
}

func (s *gormService) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	status := firstNonEmpty(req.Status, StatusRunning)
	run := &Run{
		TenantID:     req.TenantID,
		AppName:      req.AppName,
		UserID:       req.UserID,
		SessionID:    req.SessionID,
		Status:       status,
		InputSummary: req.InputSummary,
		ModelRef:     req.ModelRef,
		MetadataJSON: req.MetadataJSON,
	}
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
		if req.Status != "" {
			run.Status = req.Status
			if isTerminalStatus(req.Status) {
				now := time.Now().UTC()
				run.FinishedAt = &now
			} else if req.Status == StatusRunning || req.Status == StatusWaitingApproval {
				run.FinishedAt = nil
			}
		}
		if req.ErrorMessage != "" {
			run.ErrorMessage = req.ErrorMessage
		}
		if req.MetadataJSON != nil {
			run.MetadataJSON = *req.MetadataJSON
		}
		return nil
	})
}

func (s *gormService) CreateStep(ctx context.Context, req CreateStepRequest) (*Step, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	step := &Step{
		TenantID:    req.TenantID,
		RunID:       req.RunID,
		Kind:        firstNonEmpty(req.Kind, "unknown"),
		Status:      firstNonEmpty(req.Status, StatusRunning),
		PayloadJSON: req.PayloadJSON,
	}
	if err := s.db.WithContext(ctx).Create(step).Error; err != nil {
		return nil, err
	}
	return step, nil
}

func (s *gormService) ListSteps(ctx context.Context, tenantID, runID string) ([]Step, error) {
	var out []Step
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND run_id = ?", tenantID, runID).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *gormService) UpdateStep(ctx context.Context, tenantID, id string, req UpdateStepRequest) (*Step, error) {
	var step Step
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&step).Error
	if err != nil {
		return nil, err
	}
	if req.Status != "" {
		step.Status = req.Status
		if isTerminalStatus(req.Status) {
			now := time.Now().UTC()
			step.FinishedAt = &now
		} else if req.Status == StatusRunning || req.Status == StatusWaitingApproval {
			step.FinishedAt = nil
		}
	}
	if req.ErrorMessage != "" {
		step.ErrorMessage = req.ErrorMessage
	}
	if req.PayloadJSON != nil {
		step.PayloadJSON = *req.PayloadJSON
	}
	if err := s.db.WithContext(ctx).Save(&step).Error; err != nil {
		return nil, err
	}
	return &step, nil
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

func isTerminalStatus(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
