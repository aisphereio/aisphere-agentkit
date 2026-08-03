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
	"google.golang.org/adk/internal/platform/users"
)

// PlatformUsersAPIController exposes tenant and user records.
type PlatformUsersAPIController struct {
	service users.Service
}

func NewPlatformUsersAPIController(service users.Service) *PlatformUsersAPIController {
	return &PlatformUsersAPIController{service: service}
}

func (c *PlatformUsersAPIController) GetCurrentTenantHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	tenant, err := c.service.GetTenant(req.Context(), p.TenantID)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(tenant, http.StatusOK, rw)
}

func (c *PlatformUsersAPIController) CreateTenantHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	var body struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Status       string `json:"status"`
		Description  string `json:"description"`
		MetadataJSON string `json:"metadata_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	tenant, err := c.service.CreateTenant(req.Context(), users.CreateTenantRequest{ID: body.ID, Name: body.Name, Status: body.Status, Description: body.Description, MetadataJSON: body.MetadataJSON})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(tenant, http.StatusCreated, rw)
}

func (c *PlatformUsersAPIController) ListUsersHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	users, err := c.service.ListUsers(req.Context(), p.TenantID, limit)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(users, http.StatusOK, rw)
}

func (c *PlatformUsersAPIController) CreateUserHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		ID           string `json:"id"`
		Username     string `json:"username"`
		Email        string `json:"email"`
		DisplayName  string `json:"display_name"`
		Status       string `json:"status"`
		MetadataJSON string `json:"metadata_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	user, err := c.service.CreateUser(req.Context(), users.CreateUserRequest{TenantID: p.TenantID, ID: body.ID, Username: body.Username, Email: body.Email, DisplayName: body.DisplayName, Status: body.Status, MetadataJSON: body.MetadataJSON})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(user, http.StatusCreated, rw)
}

func (c *PlatformUsersAPIController) GetUserHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["user_id"]
	user, err := c.service.GetUser(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(user, http.StatusOK, rw)
}

func (c *PlatformUsersAPIController) UpdateUserHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["user_id"]
	var body struct {
		Username     *string `json:"username"`
		Email        *string `json:"email"`
		DisplayName  *string `json:"display_name"`
		Status       *string `json:"status"`
		MetadataJSON *string `json:"metadata_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	user, err := c.service.UpdateUser(req.Context(), p.TenantID, id, users.UpdateUserRequest{Username: body.Username, Email: body.Email, DisplayName: body.DisplayName, Status: body.Status, MetadataJSON: body.MetadataJSON})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(user, http.StatusOK, rw)
}

func (c *PlatformUsersAPIController) DeleteUserHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform user service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["user_id"]
	if id == "" {
		http.Error(rw, "user_id parameter is required", http.StatusBadRequest)
		return
	}
	if err := c.service.DeleteUser(req.Context(), p.TenantID, id); err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]bool{"ok": true}, http.StatusOK, rw)
}
