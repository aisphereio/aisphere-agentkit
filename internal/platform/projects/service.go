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
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service manages durable platform projects.
type Service interface {
	Create(ctx context.Context, req CreateRequest) (*Project, error)
	Get(ctx context.Context, tenantID, id string) (*Project, error)
	List(ctx context.Context, filter ListFilter) ([]Project, error)
	Update(ctx context.Context, tenantID, id string, req UpdateRequest) (*Project, error)
	Archive(ctx context.Context, tenantID, id string) (*Project, error)
	Delete(ctx context.Context, tenantID, id string) error
}

type CreateRequest struct {
	TenantID     string
	OwnerUserID  string
	Name         string
	DisplayName  string
	Description  string
	AppName      string
	Status       string
	MetadataJSON string
}

type ListFilter struct {
	TenantID    string
	OwnerUserID string
	AppName     string
	Status      string
	Limit       int
}

type UpdateRequest struct {
	Name         *string
	DisplayName  *string
	Description  *string
	AppName      *string
	Status       *string
	MetadataJSON *string
}

type gormService struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) Service {
	return &gormService{db: db}
}

func (s *gormService) Create(ctx context.Context, req CreateRequest) (*Project, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	project := &Project{TenantID: req.TenantID, OwnerUserID: req.OwnerUserID, Name: req.Name, DisplayName: req.DisplayName, Description: req.Description, AppName: req.AppName, Status: firstNonEmpty(req.Status, StatusActive), MetadataJSON: req.MetadataJSON}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(project).Error; err != nil {
			return err
		}
		if project.OwnerUserID != "" {
			member := ProjectMember{TenantID: project.TenantID, ProjectID: project.ID, UserID: project.OwnerUserID, Role: "owner"}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *gormService) Get(ctx context.Context, tenantID, id string) (*Project, error) {
	var project Project
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&project).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *gormService) List(ctx context.Context, filter ListFilter) ([]Project, error) {
	q := s.db.WithContext(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.OwnerUserID != "" {
		q = q.Where("owner_user_id = ?", filter.OwnerUserID)
	}
	if filter.AppName != "" {
		q = q.Where("app_name = ?", filter.AppName)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []Project
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *gormService) Update(ctx context.Context, tenantID, id string, req UpdateRequest) (*Project, error) {
	var project Project
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&project).Error; err != nil {
		return nil, err
	}
	if req.Name != nil {
		project.Name = *req.Name
	}
	if req.DisplayName != nil {
		project.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		project.Description = *req.Description
	}
	if req.AppName != nil {
		project.AppName = *req.AppName
	}
	if req.Status != nil {
		project.Status = *req.Status
	}
	if req.MetadataJSON != nil {
		project.MetadataJSON = *req.MetadataJSON
	}
	if err := s.db.WithContext(ctx).Save(&project).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *gormService) Archive(ctx context.Context, tenantID, id string) (*Project, error) {
	status := StatusArchived
	return s.Update(ctx, tenantID, id, UpdateRequest{Status: &status})
}

func (s *gormService) Delete(ctx context.Context, tenantID, id string) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("project id is required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND project_id = ?", tenantID, id).Delete(&ProjectMember{}).Error; err != nil {
			return err
		}
		result := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&Project{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
