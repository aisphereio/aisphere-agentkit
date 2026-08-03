package aihubruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		_, _ = w.Write([]byte(`{"snapshotId":"agent-snap-1","runtimeId":"runtime-1","sessionId":"session-1","agentId":"research-agent","agentVersion":"1.0.0","agentRevision":"revision-1","policy":"pinned_authorized","definition":{"entryPoint":"root_agent.yaml","files":{"root_agent.yaml":"name: research-agent\n"}},"skills":[]}`))
	}))
	defer server.Close()

	client, err := New(runtimeconfig.AIHubSkillConfig{Enabled: true, Endpoint: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := client.ResolveAgentSnapshot(WithCookieHeader(context.Background(), "aisphere_session=session-1"), "research-agent", "session-1")
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
		_, _ = w.Write([]byte(`{"snapshotId":"v1-snap","runtimeId":"runtime-1","sessionId":"session-1","agentId":"research-agent","agentVersion":"v1","agentRevision":"rev-v1","policy":"principal_passthrough_iam_enforced","definition":{"entryPoint":"root_agent.yaml","files":{"root_agent.yaml":"name: research-agent\n"}},"model":{"profile":"coding-default","model":"glm-5.2"},"tools":[{"name":"workspace.read","version":"v1","revision":"tool-rev","inputSchema":{"type":"object"}}]}`))
	}))
	defer server.Close()

	client, err := New(runtimeconfig.AIHubSkillConfig{Enabled: true, Endpoint: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := client.ResolveAgentSnapshot(context.Background(), "research-agent", "session-1")
	if err != nil {
		t.Fatalf("ResolveAgentSnapshot() error = %v", err)
	}
	if snapshot.SnapshotID != "v1-snap" || len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "workspace.read" {
		t.Fatalf("unexpected v1 snapshot: %+v", snapshot)
	}
	if snapshot.Model.Profile != "coding-default" || snapshot.Model.Model != "glm-5.2" {
		t.Fatalf("unexpected model snapshot: %+v", snapshot.Model)
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
