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

// Package auth contains platform identity primitives used by the REST layer.
package auth

import "context"

type contextKey string

const principalContextKey contextKey = "platform_principal"

// Principal is the authenticated platform identity attached to a request.
//
// It is intentionally small: user management, role expansion, and tenant policy
// live in platform services, while runtime/tool code should only need this
// request-scoped identity.
type Principal struct {
	TenantID  string   `json:"tenant_id"`
	UserID    string   `json:"user_id"`
	SessionID string   `json:"session_id,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// DefaultPrincipal returns the local-development identity used when auth.mode
// is none. Production deployments should use dev_token/local_user/OIDC modes
// instead of relying on this fallback.
func DefaultPrincipal() Principal {
	return Principal{
		TenantID: "default",
		UserID:   "admin",
		Roles:    []string{"owner"},
		Scopes:   []string{"*"},
	}
}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, p)
}

// FromContext returns the request principal. If middleware was not installed,
// it returns the local-development default so existing tests and direct handler
// usage remain stable.
func FromContext(ctx context.Context) Principal {
	if ctx != nil {
		if p, ok := ctx.Value(principalContextKey).(Principal); ok {
			return p
		}
		if p, ok := ctx.Value(principalContextKey).(*Principal); ok && p != nil {
			return *p
		}
	}
	return DefaultPrincipal()
}
