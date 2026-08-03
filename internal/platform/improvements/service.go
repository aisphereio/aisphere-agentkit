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
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Service interface {
	CreateIssue(ctx context.Context, req CreateIssueRequest) (*Issue, error)
	GetIssue(ctx context.Context, tenantID, id string) (*Issue, error)
	ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error)
	UpdateIssue(ctx context.Context, tenantID, id string, req UpdateIssueRequest) (*Issue, error)
	CreateProposal(ctx context.Context, req CreateProposalRequest) (*ProposalDetail, error)
	GetProposal(ctx context.Context, tenantID, id string) (*ProposalDetail, error)
	ListProposals(ctx context.Context, filter ListProposalsFilter) ([]Proposal, error)
	DecideProposal(ctx context.Context, tenantID, id string, req DecideProposalRequest) (*ProposalDetail, error)
	MarkProposalApplied(ctx context.Context, tenantID, id string, req ApplyProposalRequest) (*ProposalDetail, error)
}

type CreateIssueRequest struct {
	TenantID     string
	ProjectID    string
	RunID        string
	SessionID    string
	AppName      string
	AgentName    string
	IssueType    string
	Severity     string
	Title        string
	Description  string
	EvidenceJSON string
	CreatedBy    string
}

type ListIssuesFilter struct {
	TenantID  string
	ProjectID string
	RunID     string
	SessionID string
	AppName   string
	AgentName string
	IssueType string
	Severity  string
	Status    string
	Limit     int
}

type UpdateIssueRequest struct {
	Status       string
	Severity     string
	Title        string
	Description  *string
	EvidenceJSON *string
}

type CreateProposalRequest struct {
	TenantID       string
	ProjectID      string
	SourceIssueID  string
	RunID          string
	SessionID      string
	AppName        string
	Title          string
	Summary        string
	ProposalType   string
	TargetRefsJSON string
	EvidenceJSON   string
	RiskLevel      string
	Status         string
	CreatedByAgent string
	Changes        []CreateChangeRequest
}

type CreateChangeRequest struct {
	ChangeType             string
	TargetPath             string
	BeforeContentObjectKey string
	AfterContentObjectKey  string
	DiffText               string
	PatchText              string
	Status                 string
}

type ListProposalsFilter struct {
	TenantID      string
	ProjectID     string
	SourceIssueID string
	RunID         string
	SessionID     string
	AppName       string
	ProposalType  string
	RiskLevel     string
	Status        string
	Limit         int
}

type DecideProposalRequest struct {
	Status   string
	Reason   string
	Reviewer string
}

type ApplyProposalRequest struct {
	AppliedBy       string
	ApplyResultJSON string
}

type ProposalDetail struct {
	Proposal Proposal `json:"proposal"`
	Changes  []Change `json:"changes"`
}

type gormService struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) Service {
	return &gormService{db: db}
}

func (s *gormService) CreateIssue(ctx context.Context, req CreateIssueRequest) (*Issue, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	if strings.TrimSpace(req.IssueType) == "" {
		return nil, fmt.Errorf("issue_type is required")
	}
	issue := &Issue{
		TenantID:     req.TenantID,
		ProjectID:    req.ProjectID,
		RunID:        req.RunID,
		SessionID:    req.SessionID,
		AppName:      req.AppName,
		AgentName:    req.AgentName,
		IssueType:    req.IssueType,
		Severity:     firstNonEmpty(req.Severity, "medium"),
		Title:        req.Title,
		Description:  req.Description,
		EvidenceJSON: req.EvidenceJSON,
		Status:       IssueStatusOpen,
		CreatedBy:    req.CreatedBy,
	}
	if err := s.db.WithContext(ctx).Create(issue).Error; err != nil {
		return nil, err
	}
	return issue, nil
}

func (s *gormService) GetIssue(ctx context.Context, tenantID, id string) (*Issue, error) {
	var issue Issue
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&issue).Error
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func (s *gormService) ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error) {
	q := s.db.WithContext(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.ProjectID != "" {
		q = q.Where("project_id = ?", filter.ProjectID)
	}
	if filter.RunID != "" {
		q = q.Where("run_id = ?", filter.RunID)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if filter.AppName != "" {
		q = q.Where("app_name = ?", filter.AppName)
	}
	if filter.AgentName != "" {
		q = q.Where("agent_name = ?", filter.AgentName)
	}
	if filter.IssueType != "" {
		q = q.Where("issue_type = ?", filter.IssueType)
	}
	if filter.Severity != "" {
		q = q.Where("severity = ?", filter.Severity)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	var out []Issue
	err := q.Order("created_at DESC").Limit(normalizeLimit(filter.Limit)).Find(&out).Error
	return out, err
}

func (s *gormService) UpdateIssue(ctx context.Context, tenantID, id string, req UpdateIssueRequest) (*Issue, error) {
	var issue Issue
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&issue).Error
	if err != nil {
		return nil, err
	}
	if req.Status != "" {
		if !validIssueStatus(req.Status) {
			return nil, fmt.Errorf("invalid issue status %q", req.Status)
		}
		issue.Status = req.Status
	}
	if req.Severity != "" {
		issue.Severity = req.Severity
	}
	if req.Title != "" {
		issue.Title = req.Title
	}
	if req.Description != nil {
		issue.Description = *req.Description
	}
	if req.EvidenceJSON != nil {
		issue.EvidenceJSON = *req.EvidenceJSON
	}
	if err := s.db.WithContext(ctx).Save(&issue).Error; err != nil {
		return nil, err
	}
	return &issue, nil
}

func (s *gormService) CreateProposal(ctx context.Context, req CreateProposalRequest) (*ProposalDetail, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	if strings.TrimSpace(req.ProposalType) == "" {
		return nil, fmt.Errorf("proposal_type is required")
	}
	status := firstNonEmpty(req.Status, ProposalStatusPendingReview)
	if !validProposalStatus(status) {
		return nil, fmt.Errorf("invalid proposal status %q", status)
	}
	proposal := &Proposal{
		TenantID:       req.TenantID,
		ProjectID:      req.ProjectID,
		SourceIssueID:  req.SourceIssueID,
		RunID:          req.RunID,
		SessionID:      req.SessionID,
		AppName:        req.AppName,
		Title:          req.Title,
		Summary:        req.Summary,
		ProposalType:   req.ProposalType,
		TargetRefsJSON: req.TargetRefsJSON,
		EvidenceJSON:   req.EvidenceJSON,
		RiskLevel:      firstNonEmpty(req.RiskLevel, "medium"),
		Status:         status,
		CreatedByAgent: req.CreatedByAgent,
	}
	var detail ProposalDetail
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(proposal).Error; err != nil {
			return err
		}
		changes := make([]Change, 0, len(req.Changes))
		for _, in := range req.Changes {
			change := Change{
				TenantID:               req.TenantID,
				ProposalID:             proposal.ID,
				ChangeType:             firstNonEmpty(in.ChangeType, "file_patch"),
				TargetPath:             in.TargetPath,
				BeforeContentObjectKey: in.BeforeContentObjectKey,
				AfterContentObjectKey:  in.AfterContentObjectKey,
				DiffText:               in.DiffText,
				PatchText:              in.PatchText,
				Status:                 firstNonEmpty(in.Status, ChangeStatusDraft),
			}
			if err := tx.Create(&change).Error; err != nil {
				return err
			}
			changes = append(changes, change)
		}
		if proposal.SourceIssueID != "" {
			if err := tx.Model(&Issue{}).
				Where("tenant_id = ? AND id = ? AND status = ?", req.TenantID, proposal.SourceIssueID, IssueStatusOpen).
				Update("status", IssueStatusProposed).Error; err != nil {
				return err
			}
		}
		detail = ProposalDetail{Proposal: *proposal, Changes: changes}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *gormService) GetProposal(ctx context.Context, tenantID, id string) (*ProposalDetail, error) {
	var proposal Proposal
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&proposal).Error
	if err != nil {
		return nil, err
	}
	var changes []Change
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND proposal_id = ?", tenantID, id).Order("created_at ASC").Find(&changes).Error; err != nil {
		return nil, err
	}
	return &ProposalDetail{Proposal: proposal, Changes: changes}, nil
}

func (s *gormService) ListProposals(ctx context.Context, filter ListProposalsFilter) ([]Proposal, error) {
	q := s.db.WithContext(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.ProjectID != "" {
		q = q.Where("project_id = ?", filter.ProjectID)
	}
	if filter.SourceIssueID != "" {
		q = q.Where("source_issue_id = ?", filter.SourceIssueID)
	}
	if filter.RunID != "" {
		q = q.Where("run_id = ?", filter.RunID)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if filter.AppName != "" {
		q = q.Where("app_name = ?", filter.AppName)
	}
	if filter.ProposalType != "" {
		q = q.Where("proposal_type = ?", filter.ProposalType)
	}
	if filter.RiskLevel != "" {
		q = q.Where("risk_level = ?", filter.RiskLevel)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	var out []Proposal
	err := q.Order("created_at DESC").Limit(normalizeLimit(filter.Limit)).Find(&out).Error
	return out, err
}

func (s *gormService) DecideProposal(ctx context.Context, tenantID, id string, req DecideProposalRequest) (*ProposalDetail, error) {
	if req.Status != ProposalStatusApproved && req.Status != ProposalStatusRejected {
		return nil, fmt.Errorf("proposal decision status must be approved or rejected")
	}
	var proposal Proposal
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&proposal).Error
	if err != nil {
		return nil, err
	}
	if proposal.Status != ProposalStatusPendingReview && proposal.Status != ProposalStatusDraft {
		return nil, fmt.Errorf("proposal is already %s", proposal.Status)
	}
	now := time.Now().UTC()
	proposal.Status = req.Status
	proposal.ReviewedBy = req.Reviewer
	proposal.ReviewReason = req.Reason
	proposal.ReviewedAt = &now
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&proposal).Error; err != nil {
			return err
		}
		if req.Status == ProposalStatusApproved {
			return tx.Model(&Change{}).
				Where("tenant_id = ? AND proposal_id = ? AND status = ?", tenantID, id, ChangeStatusDraft).
				Update("status", ChangeStatusApproved).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetProposal(ctx, tenantID, id)
}

func (s *gormService) MarkProposalApplied(ctx context.Context, tenantID, id string, req ApplyProposalRequest) (*ProposalDetail, error) {
	var proposal Proposal
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&proposal).Error
	if err != nil {
		return nil, err
	}
	if proposal.Status != ProposalStatusApproved {
		return nil, fmt.Errorf("proposal must be approved before it can be marked applied")
	}
	now := time.Now().UTC()
	proposal.Status = ProposalStatusApplied
	proposal.AppliedBy = req.AppliedBy
	proposal.AppliedAt = &now
	proposal.ApplyResultJSON = req.ApplyResultJSON
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&proposal).Error; err != nil {
			return err
		}
		return tx.Model(&Change{}).
			Where("tenant_id = ? AND proposal_id = ? AND status = ?", tenantID, id, ChangeStatusApproved).
			Update("status", ChangeStatusApplied).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetProposal(ctx, tenantID, id)
}

func normalizeLimit(limit int) int {
	if limit <= 0 || limit > 200 {
		return 50
	}
	return limit
}

func validIssueStatus(status string) bool {
	switch status {
	case IssueStatusOpen, IssueStatusProposed, IssueStatusDismissed, IssueStatusResolved:
		return true
	default:
		return false
	}
}

func validProposalStatus(status string) bool {
	switch status {
	case ProposalStatusDraft, ProposalStatusPendingReview, ProposalStatusApproved, ProposalStatusRejected, ProposalStatusApplied, ProposalStatusFailed:
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
