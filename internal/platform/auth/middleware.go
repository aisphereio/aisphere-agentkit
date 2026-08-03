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
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/adk/internal/runtimeconfig"
)

// Middleware authenticates REST requests and injects a Principal.
//
// Supported MVP modes:
//   - none: local development; injects DefaultPrincipal and never rejects.
//   - dev_token: validates Authorization: Bearer <token> against configured
//     dev tokens or token_env values.
func Middleware(cfg runtimeconfig.AuthConfig) func(http.Handler) http.Handler {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "none"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/auth/login" || r.URL.Path == "/api/auth/login" || isPublicRuntimeMetadata(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			switch mode {
			case "", "none", "disabled", "off":
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), DefaultPrincipal())))
				return
			case "dev_token", "dev-token", "token":
				p, ok := principalForBearer(r.Header.Get("Authorization"), cfg.DevTokens)
				if !ok {
					writeAuthError(w, http.StatusUnauthorized, "unauthenticated", "missing or invalid bearer token")
					return
				}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
				return
			case "aisphere", "aisphere-auth", "aisphere_auth":
				p, err := principalForAISphereSession(r, cfg.AISphere)
				if err != nil {
					writeAuthError(w, http.StatusUnauthorized, "unauthenticated", err.Error())
					return
				}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
				return
			default:
				writeAuthError(w, http.StatusInternalServerError, "invalid_auth_config", "unsupported auth mode")
				return
			}
		})
	}
}

func isPublicRuntimeMetadata(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/models", "/tools", "/builder/defaults", "/api/models", "/api/tools", "/api/builder/defaults":
		return true
	default:
		return false
	}
}

func principalForAISphereSession(r *http.Request, cfg runtimeconfig.AISphereAuthConfig) (Principal, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.ServiceToken) == "" {
		return Principal{}, fmt.Errorf("aisphere authentication is not configured")
	}
	cookie, err := r.Cookie(cfg.CookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return Principal{}, fmt.Errorf("missing aisphere session")
	}
	body, _ := json.Marshal(map[string]string{"sessionId": cookie.Value, "app": cfg.App})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(cfg.Endpoint, "/")+"/auth/sessions/introspect", bytes.NewReader(body))
	if err != nil {
		return Principal{}, fmt.Errorf("build aisphere session request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	header := cfg.ServiceTokenHeader
	if header == "" {
		header = "X-Aisphere-Service-Token"
	}
	req.Header.Set(header, cfg.ServiceToken)
	client := &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Principal{}, fmt.Errorf("validate aisphere session: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Active    bool `json:"active"`
		Principal struct {
			SubjectID    string   `json:"subjectId"`
			Username     string   `json:"username"`
			Organization string   `json:"organization"`
			Roles        []string `json:"roles"`
			SessionID    string   `json:"sessionId"`
		} `json:"principal"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || !out.Active || strings.TrimSpace(out.Principal.Username) == "" {
		return Principal{}, fmt.Errorf("invalid aisphere session")
	}
	return Principal{TenantID: firstNonEmpty(out.Principal.Organization, "default"), UserID: out.Principal.Username, SessionID: firstNonEmpty(out.Principal.SessionID, cookie.Value), Roles: out.Principal.Roles, Scopes: []string{"*"}}, nil
}

func principalForBearer(header string, tokens []runtimeconfig.DevTokenConfig) (Principal, bool) {
	const prefix = "bearer "
	trimmed := strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
		return Principal{}, false
	}
	provided := strings.TrimSpace(trimmed[len(prefix):])
	if provided == "" {
		return Principal{}, false
	}
	for _, t := range tokens {
		expected := t.Token
		if expected == "" && t.TokenEnv != "" {
			expected = os.Getenv(t.TokenEnv)
		}
		if expected == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
			p := Principal{
				TenantID: firstNonEmpty(t.TenantID, "default"),
				UserID:   firstNonEmpty(t.UserID, "admin"),
				Roles:    append([]string(nil), t.Roles...),
				Scopes:   append([]string(nil), t.Scopes...),
			}
			if len(p.Roles) == 0 {
				p.Roles = []string{"owner"}
			}
			if len(p.Scopes) == 0 {
				p.Scopes = []string{"*"}
			}
			return p, true
		}
	}
	return Principal{}, false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
