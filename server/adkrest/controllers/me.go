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
	"net/http"

	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/runtimeconfig"
)

// MeAPIController exposes the authenticated platform principal for WebUI
// bootstrap and debugging.
type MeAPIController struct {
	cfg *runtimeconfig.Config
}

// NewMeAPIController creates a new MeAPIController.
func NewMeAPIController(cfg *runtimeconfig.Config) *MeAPIController {
	return &MeAPIController{cfg: cfg}
}

// MeHandler handles GET /me.
func (c *MeAPIController) MeHandler(rw http.ResponseWriter, req *http.Request) {
	p := auth.FromContext(req.Context())
	authMode := "none"
	if c != nil && c.cfg != nil && c.cfg.Auth.Mode != "" {
		authMode = c.cfg.Auth.Mode
	}
	EncodeJSONResponse(map[string]any{
		"tenant_id": p.TenantID,
		"user_id":   p.UserID,
		"roles":     p.Roles,
		"scopes":    p.Scopes,
		"auth_mode": authMode,
	}, http.StatusOK, rw)
}
