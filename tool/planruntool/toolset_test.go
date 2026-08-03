package planruntool

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
func (c *testToolContext) AgentName() string           { return "book_skill_runner" }
func (c *testToolContext) ReadonlyState() session.ReadonlyState {
	return c.state
}
func (c *testToolContext) UserID() string    { return "user" }
func (c *testToolContext) AppName() string   { return "book_skill_runner" }
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

func TestPlanRunStartNextRecordAndResumeByLatest(t *testing.T) {
	ctx := &testToolContext{
		Context: context.Background(),
		artifacts: &artifactinternal.Artifacts{
			Service:   artifact.InMemoryService(),
			AppName:   "book_skill_runner",
			UserID:    "user",
			SessionID: "session",
		},
		state: mapState{"project_id": "demo_project"},
	}
	ts := &Toolset{}

	start, err := ts.Start(ctx, StartArgs{
		PlanType:      "book_skill_loop",
		Objective:     "extract dialogue skill",
		MaxIterations: 2,
		Metadata:      map[string]string{"skill_id": "novel-dialogue-power-dynamics"},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if start.State.Status != statusQueued {
		t.Fatalf("status = %q, want queued", start.State.Status)
	}

	next, err := ts.NextIteration(ctx, NextIterationArgs{PlanRunID: start.State.PlanRunID})
	if err != nil {
		t.Fatalf("NextIteration() error = %v", err)
	}
	if !next.CanContinue || next.Iteration != 1 {
		t.Fatalf("NextIteration() = iteration %d continue %v, want 1 true", next.Iteration, next.CanContinue)
	}

	recorded, err := ts.RecordIteration(ctx, RecordIterationArgs{
		PlanRunID:       start.State.PlanRunID,
		Iteration:       1,
		Status:          iterationCompleted,
		SourceArtifacts: []string{"user:demo_book__chapter_0001.txt"},
		OutputArtifacts: []string{"user:skill_v001.md"},
		DomainRunID:     "book_run_1",
		CurrentPointer:  "user:skill_v001.md",
	})
	if err != nil {
		t.Fatalf("RecordIteration() error = %v", err)
	}
	if recorded.State.CompletedIterations != 1 {
		t.Fatalf("CompletedIterations = %d, want 1", recorded.State.CompletedIterations)
	}

	loaded, err := ts.Get(ctx, LookupArgs{ProjectID: "demo_project", PlanType: "book_skill_loop"})
	if err != nil {
		t.Fatalf("Get(latest) error = %v", err)
	}
	if loaded.State.PlanRunID != start.State.PlanRunID {
		t.Fatalf("latest plan_run_id = %q, want %q", loaded.State.PlanRunID, start.State.PlanRunID)
	}
}
