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
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Service interface {
	Create(ctx context.Context, req CreateRequest) (*Request, error)
	Get(ctx context.Context, tenantID, id string) (*Request, error)
	List(ctx context.Context, filter ListFilter) ([]Request, error)
	Decide(ctx context.Context, tenantID, id string, req DecideRequest) (*Request, error)
}

type CreateRequest struct {
	TenantID    string
	RunID       string
	SessionID   string
	UserID      string
	Kind        string
	PayloadJSON string
}

type ListFilter struct {
	TenantID  string
	RunID     string
	SessionID string
	UserID    string
	Status    string
	Kind      string
	Limit     int
}

type DecideRequest struct {
	Status    string
	Reason    string
	DecidedBy string
}

type gormService struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) Service {
	return &gormService{db: db}
}

func (s *gormService) Create(ctx context.Context, req CreateRequest) (*Request, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.Kind) == "" {
		return nil, fmt.Errorf("kind is required")
	}
	request := &Request{
		TenantID:    req.TenantID,
		RunID:       req.RunID,
		SessionID:   req.SessionID,
		UserID:      req.UserID,
		Kind:        req.Kind,
		Status:      StatusPending,
		PayloadJSON: req.PayloadJSON,
	}
	if err := s.db.WithContext(ctx).Create(request).Error; err != nil {
		return nil, err
	}
	return request, nil
}

func (s *gormService) Get(ctx context.Context, tenantID, id string) (*Request, error) {
	var request Request
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&request).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *gormService) List(ctx context.Context, filter ListFilter) ([]Request, error) {
	q := s.db.WithContext(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.RunID != "" {
		q = q.Where("run_id = ?", filter.RunID)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Kind != "" {
		q = q.Where("kind = ?", filter.Kind)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []Request
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *gormService) Decide(ctx context.Context, tenantID, id string, req DecideRequest) (*Request, error) {
	status := req.Status
	if status != StatusApproved && status != StatusRejected && status != StatusExpired {
		return nil, fmt.Errorf("status must be approved, rejected, or expired")
	}

	var request Request
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&request).Error
	if err != nil {
		return nil, err
	}
	if request.Status != StatusPending {
		return nil, fmt.Errorf("approval request is already %s", request.Status)
	}
	now := time.Now().UTC()
	request.Status = status
	request.Reason = req.Reason
	request.DecidedBy = req.DecidedBy
	request.DecidedAt = &now
	if err := s.db.WithContext(ctx).Save(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}
