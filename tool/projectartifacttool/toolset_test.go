package projectartifacttool

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	artifactinternal "google.golang.org/adk/internal/artifact"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
)

type testToolContext struct {
	context.Context
	artifacts agent.Artifacts
	state     session.State
}

func (c *testToolContext) UserContent() *genai.Content { return nil }
func (c *testToolContext) InvocationID() string        { return "invocation" }
func (c *testToolContext) AgentName() string           { return "project_artifact_test" }
func (c *testToolContext) ReadonlyState() session.ReadonlyState {
	return nil
}
func (c *testToolContext) UserID() string    { return "user" }
func (c *testToolContext) AppName() string   { return "project_artifact_test" }
func (c *testToolContext) SessionID() string { return "session" }
func (c *testToolContext) Branch() string    { return "" }
func (c *testToolContext) Artifacts() agent.Artifacts {
	return c.artifacts
}
func (c *testToolContext) State() session.State { return c.state }
func (c *testToolContext) FunctionCallID() string {
	return "call"
}
func (c *testToolContext) Actions() *session.EventActions {
	return &session.EventActions{}
}
func (c *testToolContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return &memory.SearchResponse{}, nil
}
func (c *testToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return nil
}
func (c *testToolContext) RequestConfirmation(string, any) error {
	return nil
}

type mapState map[string]any

func (s mapState) Get(key string) (any, error) {
	v, ok := s[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

func (s mapState) Set(key string, value any) error {
	s[key] = value
	return nil
}

func (s mapState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s {
			if !yield(k, v) {
				return
			}
		}
	}
}

func TestRegisterArtifactIdempotentForUnchangedEntry(t *testing.T) {
	service := artifact.InMemoryService()
	ctx := &testToolContext{
		Context: context.Background(),
		artifacts: &artifactinternal.Artifacts{
			Service:   service,
			AppName:   "project_artifact_test",
			UserID:    "user",
			SessionID: "session",
		},
	}

	if _, _, err := EnsureProject(ctx, EnsureProjectRequest{
		ProjectID:   "demo_project",
		Name:        "demo_project",
		DisplayName: "Demo Project",
	}); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}

	req := RegisterArtifactRequest{
		ProjectID:        "demo_project",
		ArtifactName:     "user:demo_book__manifest.json",
		Type:             "book.manifest",
		Title:            "Demo Book Manifest",
		Visibility:       VisibilityProjectDefault,
		Mountable:        boolPtr(true),
		DefaultForAgents: []string{"book_skill_runner"},
		BookID:           "demo_book",
		Metadata:         map[string]string{"chapter_count": "2"},
	}
	if _, _, err := RegisterArtifact(ctx, req); err != nil {
		t.Fatalf("first RegisterArtifact() error = %v", err)
	}
	if _, _, err := RegisterArtifact(ctx, req); err != nil {
		t.Fatalf("second RegisterArtifact() error = %v", err)
	}

	versions, err := ctx.Artifacts().Versions(t.Context(), registryArtifactName("demo_project"))
	if err != nil {
		t.Fatalf("registry versions: %v", err)
	}
	if got, want := len(versions.Versions), 2; got != want {
		t.Fatalf("registry version count = %d, want %d; versions=%v", got, want, versions.Versions)
	}
}

func TestMountProjectBindsSessionState(t *testing.T) {
	service := artifact.InMemoryService()
	state := mapState{}
	ctx := &testToolContext{
		Context: context.Background(),
		artifacts: &artifactinternal.Artifacts{
			Service:   service,
			AppName:   "project_artifact_test",
			UserID:    "user",
			SessionID: "session",
		},
		state: state,
	}

	if _, err := MountProject(ctx, ProjectRegistry{ProjectID: "demo_project", Name: "demo_project"}); err != nil {
		t.Fatalf("MountProject() error = %v", err)
	}
	if got, want := state["project_id"], "demo_project"; got != want {
		t.Fatalf("project_id state = %v, want %q", got, want)
	}
	if got, want := state["projectId"], "demo_project"; got != want {
		t.Fatalf("projectId state = %v, want %q", got, want)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
