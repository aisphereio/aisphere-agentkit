package session

import (
	"context"
	"os"
	"testing"
)

func TestPostgresSessionEventRoundTrip(t *testing.T) {
	dsn := os.Getenv("AGENTKIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGENTKIT_POSTGRES_DSN is not set")
	}
	svc, err := PostgresService(context.Background(), PostgresConfig{DSN: dsn, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.Create(context.Background(), &CreateRequest{AppName: "agent-it", UserID: "user-it", SessionID: "sess-it", State: map[string]any{"projectId": "project-it"}})
	if err != nil {
		t.Fatal(err)
	}
	ev := NewEvent("inv-it")
	ev.Author = "assistant"
	ev.Actions.StateDelta = map[string]any{"last": "ok"}
	if err := svc.AppendEvent(context.Background(), resp.Session, ev); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), &GetRequest{AppName: "agent-it", UserID: "user-it", SessionID: "sess-it"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Events().Len() != 1 {
		t.Fatalf("expected one event, got %d", got.Session.Events().Len())
	}
}
