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

package sessionnative

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/sandboxclient"
)

func TestEnsureSessionAcceptsToolsEndpointWithoutWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sandboxes/ensure", "/v1/sandboxes/sandbox-1":
			_, _ = w.Write([]byte(`{"sandboxId":"sandbox-1","phase":"ready","toolsEndpoint":"http://sandbox-tools"}`))
		case "/v1/sandboxes/sandbox-1/tools":
			_, _ = w.Write([]byte(`{"sandboxId":"sandbox-1","tools":[]}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	manager := &Manager{
		Sandbox:        sandboxclient.New(server.URL, ""),
		DefaultProfile: "agent-default",
	}
	lease, err := manager.EnsureSession(t.Context(), CreateSessionRequest{AgentID: "agent-1", SessionID: "session-1"})
	if err != nil {
		t.Fatalf("EnsureSession() error = %v", err)
	}
	if lease.Sandbox.ToolsEndpoint == "" {
		t.Fatalf("ToolsEndpoint is empty")
	}
}

func TestEnsureSessionCanBootstrapBeforeHubApprovalResolve(t *testing.T) {
	hubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Hub resolve should be deferred during session bootstrap: %s", r.URL.Path)
	}))
	defer hubServer.Close()
	hub, err := aihubruntime.New(runtimeconfig.AIHubSkillConfig{Enabled: true, Endpoint: hubServer.URL})
	if err != nil {
		t.Fatalf("create Hub client: %v", err)
	}
	sandboxServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tools") {
			_, _ = w.Write([]byte(`{"sandboxId":"sandbox-1","tools":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"sandboxId":"sandbox-1","phase":"ready","toolsEndpoint":"http://sandbox-tools"}`))
	}))
	defer sandboxServer.Close()
	manager := &Manager{
		Sandbox: sandboxclient.New(sandboxServer.URL, ""), Hub: hub,
		DefaultProfile: "agent-default",
	}
	lease, err := manager.EnsureSession(t.Context(), CreateSessionRequest{
		AgentID: "agent-1", SessionID: "session-1", SkipAgentResolve: true,
	})
	if err != nil {
		t.Fatalf("EnsureSession() error = %v", err)
	}
	if lease.Plan != nil {
		t.Fatalf("bootstrap lease unexpectedly contains a runtime plan: %+v", lease.Plan)
	}
}
