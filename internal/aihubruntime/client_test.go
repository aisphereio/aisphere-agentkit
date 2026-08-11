// Copyright 2025 Google LLC
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

package aihubruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestSnapshotFromRefsPreservesDownloadURL(t *testing.T) {
	client := &Client{}
	snapshot := client.snapshotFromRefs("team-skills", "revision-1", "snapshot-1", "2026-06-20T00:00:00Z", "", "new_sessions_only", []skillRef{{
		Name:        "release-notes",
		Version:     "v1",
		DownloadURL: "/v3/client/ai/skills/release-notes?version=v1",
	}})

	if len(snapshot.Skills) != 1 {
		t.Fatalf("snapshot skills = %d, want 1", len(snapshot.Skills))
	}
	if got, want := snapshot.Skills[0].DownloadURL, "/v3/client/ai/skills/release-notes?version=v1"; got != want {
		t.Fatalf("download URL = %q, want %q", got, want)
	}
}

func TestResolveAgentSnapshotForwardsSessionCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "aisphere_session=session-1" {
			t.Fatalf("Cookie = %q", got)
		}
		if r.URL.Path != "/v3/aihub/runtime/agents/research-agent/resolve" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Aisphere-Principal-JWT"); got != "signed-principal" {
			t.Fatalf("principal JWT = %q, want signed-principal", got)
		}
		_, _ = w.Write([]byte(`{"snapshotId":"agent-snap-1","runtimeId":"runtime-1","sessionId":"session-1","agentId":"research-agent","agentVersion":"1.0.0","agentRevision":"revision-1","policy":"pinned_authorized","definition":{"entryPoint":"root_agent.yaml","files":{"root_agent.yaml":"name: research-agent\n"}},"skills":[]}`))
	}))
	defer server.Close()

	client, err := New(runtimeconfig.AIHubSkillConfig{Enabled: true, Endpoint: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	forwarded := make(http.Header)
	forwarded.Set("X-Aisphere-Principal-JWT", "signed-principal")
	ctx := WithRequestHeaders(context.Background(), forwarded)
	snapshot, err := client.ResolveAgentSnapshot(WithCookieHeader(ctx, "aisphere_session=session-1"), "research-agent", "session-1")
	if err != nil {
		t.Fatalf("ResolveAgentSnapshot() error = %v", err)
	}
	if snapshot.AgentVersion != "1.0.0" {
		t.Fatalf("AgentVersion = %q, want 1.0.0", snapshot.AgentVersion)
	}
}

func TestResolveAgentSnapshotFallsBackToKernelizedV1Hub(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/aihub/runtime/agents/research-agent/resolve" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/agents/research-agent:resolve" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["approvalConfirmed"] != true || request["version"] != "v1" {
			t.Fatalf("approval options were not forwarded: %+v", request)
		}
		_, _ = w.Write([]byte(`{"snapshotId":"v1-snap","runtimeId":"runtime-1","sessionId":"session-1","agentId":"research-agent","agentVersion":"v1","agentRevision":"rev-v1","policy":"principal_passthrough_iam_enforced","definition":{"entryPoint":"root_agent.yaml","files":{"root_agent.yaml":"name: research-agent\n"}},"model":{"profile":"coding-default","model":"glm-5.2"},"authorization":{"approvalConfirmed":true,"tools":[{"tool":"workspace.read","approvalMode":"always","approved":true}]},"tools":[{"name":"workspace.read","version":"v1","revision":"tool-rev","inputSchema":{"type":"object"}}]}`))
	}))
	defer server.Close()

	client, err := New(runtimeconfig.AIHubSkillConfig{Enabled: true, Endpoint: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := client.ResolveAgentSnapshotWithOptions(context.Background(), "research-agent", "session-1", AgentResolveOptions{
		ApprovalConfirmed: true, ApprovedTools: []string{"workspace.read"}, Version: "v1",
	})
	if err != nil {
		t.Fatalf("ResolveAgentSnapshot() error = %v", err)
	}
	if snapshot.SnapshotID != "v1-snap" || len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "workspace.read" {
		t.Fatalf("unexpected v1 snapshot: %+v", snapshot)
	}
	if snapshot.Model.Profile != "coding-default" || snapshot.Model.Model != "glm-5.2" {
		t.Fatalf("unexpected model snapshot: %+v", snapshot.Model)
	}
	if !snapshot.Authorization["approvalConfirmed"].(bool) {
		t.Fatalf("authorization was not preserved: %+v", snapshot.Authorization)
	}
}

func TestCookieForwardingIsLimitedToHubEndpoint(t *testing.T) {
	client := &Client{cfg: runtimeconfig.AIHubSkillConfig{Endpoint: "https://hub.example.test"}}
	ctx := WithCookieHeader(context.Background(), "aisphere_session=session-1")
	hubRequest := httptest.NewRequest(http.MethodGet, "https://hub.example.test/v3/aihub/agents", nil)
	client.applyCookieHeader(ctx, hubRequest)
	if got := hubRequest.Header.Get("Cookie"); got != "aisphere_session=session-1" {
		t.Fatalf("Hub Cookie = %q", got)
	}
	externalRequest := httptest.NewRequest(http.MethodGet, "https://objects.example.test/agent.zip", nil)
	client.applyCookieHeader(ctx, externalRequest)
	if got := externalRequest.Header.Get("Cookie"); got != "" {
		t.Fatalf("external Cookie = %q, want empty", got)
	}
}

func TestForwardedPrincipalJWTIsApplied(t *testing.T) {
	client := &Client{}
	forwarded := make(http.Header)
	forwarded.Set("X-Aisphere-Principal-JWT", "signed-principal")
	ctx := WithRequestHeaders(context.Background(), forwarded)
	if got := requestHeadersFromContext(ctx)["X-Aisphere-Principal-JWT"]; got != "signed-principal" {
		t.Fatalf("context principal JWT = %q, want signed-principal", got)
	}
	req := httptest.NewRequest(http.MethodGet, "https://hub.example.test/healthz", nil).WithContext(ctx)
	client.applyForwardedPrincipalHeaders(req)
	if got := req.Header.Get("X-Aisphere-Principal-JWT"); got != "signed-principal" {
		t.Fatalf("principal JWT = %q, want signed-principal", got)
	}
}

func TestPrepareAgentSnapshotSkillRootUsesPinnedCache(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".aihub", "skills", "demo", "versions", "v1")
	if err := os.MkdirAll(filepath.Join(cache, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "SKILL.md"), []byte("---\nname: demo\n---\nUse demo."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "references", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := &AgentSnapshot{SnapshotID: "snap-1", SessionID: "session-1", Skills: []SkillSnapshotItem{{Name: "demo", Version: "v1", CachePath: cache}}}
	got, err := PrepareAgentSnapshotSkillRoot(root, snapshot)
	if err != nil {
		t.Fatalf("PrepareAgentSnapshotSkillRoot() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, "demo", "SKILL.md")); err != nil {
		t.Fatalf("materialized SKILL.md missing: %v", err)
	}
	if snapshot.Skills[0].MountPath == "" {
		t.Fatal("snapshot skill mount path is empty")
	}
}
