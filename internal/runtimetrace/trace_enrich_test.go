package runtimetrace

import (
	"context"
	"testing"
	"time"
)

type readonlyTraceContext struct {
	context.Context
}

func (readonlyTraceContext) InvocationID() string { return "inv-1" }
func (readonlyTraceContext) AppName() string      { return "app-1" }
func (readonlyTraceContext) UserID() string       { return "user-1" }
func (readonlyTraceContext) SessionID() string    { return "session-1" }
func (readonlyTraceContext) AgentName() string    { return "agent-1" }
func (readonlyTraceContext) Branch() string       { return "branch-1" }

func TestEnrichReadonlyContext(t *testing.T) {
	ctx := WithRunID(readonlyTraceContext{Context: context.Background()}, "run-1")
	ev := Enrich(ctx, Event{Type: EventSubAgentTaskStarted, Time: time.Now()})
	if ev.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", ev.RunID)
	}
	if ev.InvocationID != "inv-1" || ev.AppName != "app-1" || ev.UserID != "user-1" || ev.SessionID != "session-1" || ev.AgentName != "agent-1" || ev.Branch != "branch-1" {
		t.Fatalf("unexpected enriched event: %+v", ev)
	}
}
