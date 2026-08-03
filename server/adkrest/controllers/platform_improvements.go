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

package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/improvements"
)

// PlatformImprovementsAPIController exposes runtime-driven improvement issues,
// proposals, and review decisions.
type PlatformImprovementsAPIController struct {
	service improvements.Service
}

func NewPlatformImprovementsAPIController(service improvements.Service) *PlatformImprovementsAPIController {
	return &PlatformImprovementsAPIController{service: service}
}

func (c *PlatformImprovementsAPIController) ListIssuesHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := c.service.ListIssues(req.Context(), improvements.ListIssuesFilter{
		TenantID:  p.TenantID,
		ProjectID: q.Get("project_id"),
		RunID:     q.Get("run_id"),
		SessionID: q.Get("session_id"),
		AppName:   q.Get("app_name"),
		AgentName: q.Get("agent_name"),
		IssueType: q.Get("issue_type"),
		Severity:  q.Get("severity"),
		Status:    q.Get("status"),
		Limit:     limit,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformImprovementsAPIController) CreateIssueHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		ProjectID    string `json:"project_id"`
		RunID        string `json:"run_id"`
		SessionID    string `json:"session_id"`
		AppName      string `json:"app_name"`
		AgentName    string `json:"agent_name"`
		IssueType    string `json:"issue_type"`
		Severity     string `json:"severity"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		EvidenceJSON string `json:"evidence_json"`
		CreatedBy    string `json:"created_by"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	createdBy := body.CreatedBy
	if createdBy == "" {
		createdBy = p.UserID
	}
	out, err := c.service.CreateIssue(req.Context(), improvements.CreateIssueRequest{
		TenantID:     p.TenantID,
		ProjectID:    body.ProjectID,
		RunID:        body.RunID,
		SessionID:    body.SessionID,
		AppName:      body.AppName,
		AgentName:    body.AgentName,
		IssueType:    body.IssueType,
		Severity:     body.Severity,
		Title:        body.Title,
		Description:  body.Description,
		EvidenceJSON: body.EvidenceJSON,
		CreatedBy:    createdBy,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(out, http.StatusCreated, rw)
}

func (c *PlatformImprovementsAPIController) GetIssueHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["issue_id"]
	out, err := c.service.GetIssue(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformImprovementsAPIController) UpdateIssueHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["issue_id"]
	var body struct {
		Status       string  `json:"status"`
		Severity     string  `json:"severity"`
		Title        string  `json:"title"`
		Description  *string `json:"description"`
		EvidenceJSON *string `json:"evidence_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	out, err := c.service.UpdateIssue(req.Context(), p.TenantID, id, improvements.UpdateIssueRequest{
		Status:       body.Status,
		Severity:     body.Severity,
		Title:        body.Title,
		Description:  body.Description,
		EvidenceJSON: body.EvidenceJSON,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformImprovementsAPIController) ListProposalsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := c.service.ListProposals(req.Context(), improvements.ListProposalsFilter{
		TenantID:      p.TenantID,
		ProjectID:     q.Get("project_id"),
		SourceIssueID: q.Get("source_issue_id"),
		RunID:         q.Get("run_id"),
		SessionID:     q.Get("session_id"),
		AppName:       q.Get("app_name"),
		ProposalType:  q.Get("proposal_type"),
		RiskLevel:     q.Get("risk_level"),
		Status:        q.Get("status"),
		Limit:         limit,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformImprovementsAPIController) CreateProposalHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		ProjectID      string                             `json:"project_id"`
		SourceIssueID  string                             `json:"source_issue_id"`
		RunID          string                             `json:"run_id"`
		SessionID      string                             `json:"session_id"`
		AppName        string                             `json:"app_name"`
		Title          string                             `json:"title"`
		Summary        string                             `json:"summary"`
		ProposalType   string                             `json:"proposal_type"`
		TargetRefsJSON string                             `json:"target_refs_json"`
		EvidenceJSON   string                             `json:"evidence_json"`
		RiskLevel      string                             `json:"risk_level"`
		Status         string                             `json:"status"`
		CreatedByAgent string                             `json:"created_by_agent"`
		Changes        []improvements.CreateChangeRequest `json:"changes"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	out, err := c.service.CreateProposal(req.Context(), improvements.CreateProposalRequest{
		TenantID:       p.TenantID,
		ProjectID:      body.ProjectID,
		SourceIssueID:  body.SourceIssueID,
		RunID:          body.RunID,
		SessionID:      body.SessionID,
		AppName:        body.AppName,
		Title:          body.Title,
		Summary:        body.Summary,
		ProposalType:   body.ProposalType,
		TargetRefsJSON: body.TargetRefsJSON,
		EvidenceJSON:   body.EvidenceJSON,
		RiskLevel:      body.RiskLevel,
		Status:         body.Status,
		CreatedByAgent: body.CreatedByAgent,
		Changes:        body.Changes,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(out, http.StatusCreated, rw)
}

func (c *PlatformImprovementsAPIController) GetProposalHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["proposal_id"]
	out, err := c.service.GetProposal(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformImprovementsAPIController) ApproveProposalHandler(rw http.ResponseWriter, req *http.Request) {
	c.decideProposal(rw, req, improvements.ProposalStatusApproved)
}

func (c *PlatformImprovementsAPIController) RejectProposalHandler(rw http.ResponseWriter, req *http.Request) {
	c.decideProposal(rw, req, improvements.ProposalStatusRejected)
}

func (c *PlatformImprovementsAPIController) MarkProposalAppliedHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["proposal_id"]
	var body struct {
		ApplyResultJSON string `json:"apply_result_json"`
	}
	if req.Body != nil && req.ContentLength != 0 {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}
	out, err := c.service.MarkProposalApplied(req.Context(), p.TenantID, id, improvements.ApplyProposalRequest{
		AppliedBy:       p.UserID,
		ApplyResultJSON: body.ApplyResultJSON,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformImprovementsAPIController) decideProposal(rw http.ResponseWriter, req *http.Request, status string) {
	if c.service == nil {
		http.Error(rw, "platform improvement service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["proposal_id"]
	var body struct {
		Reason string `json:"reason"`
	}
	if req.Body != nil && req.ContentLength != 0 {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}
	out, err := c.service.DecideProposal(req.Context(), p.TenantID, id, improvements.DecideProposalRequest{
		Status:   status,
		Reason:   body.Reason,
		Reviewer: p.UserID,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}
