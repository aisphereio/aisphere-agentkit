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

	"google.golang.org/adk/internal/platform/approvals"
	"google.golang.org/adk/internal/platform/auth"
)

// PlatformApprovalsAPIController exposes human-in-the-loop approval records.
type PlatformApprovalsAPIController struct {
	service approvals.Service
}

func NewPlatformApprovalsAPIController(service approvals.Service) *PlatformApprovalsAPIController {
	return &PlatformApprovalsAPIController{service: service}
}

func (c *PlatformApprovalsAPIController) ListApprovalsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform approval service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := c.service.List(req.Context(), approvals.ListFilter{
		TenantID:  p.TenantID,
		RunID:     q.Get("run_id"),
		SessionID: q.Get("session_id"),
		UserID:    q.Get("user_id"),
		Status:    q.Get("status"),
		Kind:      q.Get("kind"),
		Limit:     limit,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformApprovalsAPIController) CreateApprovalHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform approval service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		RunID       string `json:"run_id"`
		SessionID   string `json:"session_id"`
		UserID      string `json:"user_id"`
		Kind        string `json:"kind"`
		PayloadJSON string `json:"payload_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	userID := body.UserID
	if userID == "" {
		userID = p.UserID
	}
	out, err := c.service.Create(req.Context(), approvals.CreateRequest{
		TenantID:    p.TenantID,
		RunID:       body.RunID,
		SessionID:   body.SessionID,
		UserID:      userID,
		Kind:        body.Kind,
		PayloadJSON: body.PayloadJSON,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(out, http.StatusCreated, rw)
}

func (c *PlatformApprovalsAPIController) GetApprovalHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform approval service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["approval_id"]
	out, err := c.service.Get(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformApprovalsAPIController) ApproveHandler(rw http.ResponseWriter, req *http.Request) {
	c.decide(rw, req, approvals.StatusApproved)
}

func (c *PlatformApprovalsAPIController) RejectHandler(rw http.ResponseWriter, req *http.Request) {
	c.decide(rw, req, approvals.StatusRejected)
}

func (c *PlatformApprovalsAPIController) ExpireHandler(rw http.ResponseWriter, req *http.Request) {
	c.decide(rw, req, approvals.StatusExpired)
}

func (c *PlatformApprovalsAPIController) decide(rw http.ResponseWriter, req *http.Request, status string) {
	if c.service == nil {
		http.Error(rw, "platform approval service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["approval_id"]
	var body struct {
		Reason string `json:"reason"`
	}
	if req.Body != nil && req.ContentLength != 0 {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}
	out, err := c.service.Decide(req.Context(), p.TenantID, id, approvals.DecideRequest{
		Status:    status,
		Reason:    body.Reason,
		DecidedBy: p.UserID,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(out, http.StatusOK, rw)
}
