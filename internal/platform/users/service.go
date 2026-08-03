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

package users

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service manages durable platform tenants and users.
type Service interface {
	BootstrapPrincipal(ctx context.Context, tenantID, userID string, roles []string) error
	CreateTenant(ctx context.Context, req CreateTenantRequest) (*Tenant, error)
	GetTenant(ctx context.Context, tenantID string) (*Tenant, error)
	ListUsers(ctx context.Context, tenantID string, limit int) ([]User, error)
	CreateUser(ctx context.Context, req CreateUserRequest) (*User, error)
	GetUser(ctx context.Context, tenantID, userID string) (*User, error)
	UpdateUser(ctx context.Context, tenantID, userID string, req UpdateUserRequest) (*User, error)
	DeleteUser(ctx context.Context, tenantID, userID string) error
}

type CreateTenantRequest struct {
	ID           string
	Name         string
	Status       string
	Description  string
	MetadataJSON string
}

type CreateUserRequest struct {
	TenantID     string
	ID           string
	Username     string
	Email        string
	DisplayName  string
	Status       string
	MetadataJSON string
}

type UpdateUserRequest struct {
	Username     *string
	Email        *string
	DisplayName  *string
	Status       *string
	MetadataJSON *string
}

type gormService struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) Service {
	return &gormService{db: db}
}

func (s *gormService) BootstrapPrincipal(ctx context.Context, tenantID, userID string, roles []string) error {
	tenantID = firstNonEmpty(tenantID, "default")
	userID = firstNonEmpty(userID, "admin")
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := Tenant{ID: tenantID, Name: tenantID, Status: StatusActive}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tenant).Error; err != nil {
			return err
		}
		user := User{TenantID: tenantID, ID: userID, Username: userID, DisplayName: userID, Status: StatusActive}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&user).Error; err != nil {
			return err
		}
		for _, roleName := range roles {
			roleName = strings.TrimSpace(roleName)
			if roleName == "" {
				continue
			}
			role := Role{TenantID: tenantID, ID: roleName, Name: roleName}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&role).Error; err != nil {
				return err
			}
			assignment := UserRole{TenantID: tenantID, UserID: userID, RoleID: roleName}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assignment).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *gormService) CreateTenant(ctx context.Context, req CreateTenantRequest) (*Tenant, error) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	tenant := &Tenant{ID: req.ID, Name: req.Name, Status: firstNonEmpty(req.Status, StatusActive), Description: req.Description, MetadataJSON: req.MetadataJSON}
	if err := s.db.WithContext(ctx).Create(tenant).Error; err != nil {
		return nil, err
	}
	return tenant, nil
}

func (s *gormService) GetTenant(ctx context.Context, tenantID string) (*Tenant, error) {
	var tenant Tenant
	if err := s.db.WithContext(ctx).Where("id = ?", tenantID).First(&tenant).Error; err != nil {
		return nil, err
	}
	return &tenant, nil
}

func (s *gormService) ListUsers(ctx context.Context, tenantID string, limit int) ([]User, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []User
	err := s.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *gormService) CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("user id is required")
	}
	user := &User{TenantID: req.TenantID, ID: req.ID, Username: req.Username, Email: req.Email, DisplayName: req.DisplayName, Status: firstNonEmpty(req.Status, StatusActive), MetadataJSON: req.MetadataJSON}
	if err := s.db.WithContext(ctx).Create(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func (s *gormService) GetUser(ctx context.Context, tenantID, userID string) (*User, error) {
	var user User
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, userID).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *gormService) UpdateUser(ctx context.Context, tenantID, userID string, req UpdateUserRequest) (*User, error) {
	var user User
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, userID).First(&user).Error; err != nil {
		return nil, err
	}
	if req.Username != nil {
		user.Username = *req.Username
	}
	if req.Email != nil {
		user.Email = *req.Email
	}
	if req.DisplayName != nil {
		user.DisplayName = *req.DisplayName
	}
	if req.Status != nil {
		user.Status = *req.Status
	}
	if req.MetadataJSON != nil {
		user.MetadataJSON = *req.MetadataJSON
	}
	if err := s.db.WithContext(ctx).Save(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// DeleteUser soft-deletes a user by archiving it. This keeps historical sessions,
// runs, approvals, and artifacts referentially readable for admin/audit views.
func (s *gormService) DeleteUser(ctx context.Context, tenantID, userID string) error {
	archived := StatusArchived
	_, err := s.UpdateUser(ctx, tenantID, userID, UpdateUserRequest{Status: &archived})
	return err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
