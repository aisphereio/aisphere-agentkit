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

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestMiddlewareNoneInjectsDefaultPrincipal(t *testing.T) {
	h := Middleware(runtimeconfig.AuthConfig{Mode: "none"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromContext(r.Context())
		if p.TenantID != "default" || p.UserID != "admin" {
			t.Fatalf("principal = %+v, want default/admin", p)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestMiddlewareDevTokenAcceptsConfiguredBearer(t *testing.T) {
	cfg := runtimeconfig.AuthConfig{
		Mode: "dev_token",
		DevTokens: []runtimeconfig.DevTokenConfig{{
			Token:    "secret",
			TenantID: "tenant-a",
			UserID:   "user-a",
			Roles:    []string{"developer"},
			Scopes:   []string{"sessions:read"},
		}},
	}
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromContext(r.Context())
		if p.TenantID != "tenant-a" || p.UserID != "user-a" {
			t.Fatalf("principal = %+v, want tenant-a/user-a", p)
		}
		if len(p.Roles) != 1 || p.Roles[0] != "developer" {
			t.Fatalf("roles = %+v, want developer", p.Roles)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestMiddlewareDevTokenRejectsMissingBearer(t *testing.T) {
	cfg := runtimeconfig.AuthConfig{
		Mode:      "dev_token",
		DevTokens: []runtimeconfig.DevTokenConfig{{Token: "secret"}},
	}
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
