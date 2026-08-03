package runtimeconfig

import (
	"context"
	"path/filepath"
	"testing"

	"google.golang.org/adk/session"
)

func TestBuildServicesSQLiteSessionBackend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Default(dir)
	cfg.Storage.Session = ServiceConfig{
		Type:        "sqlite",
		DSN:         filepath.Join(dir, "sessions", "adk-session.db"),
		AutoMigrate: true,
	}
	cfg.normalize(dir)

	services, err := cfg.BuildServices()
	if err != nil {
		t.Fatalf("BuildServices() error = %v", err)
	}

	ctx := context.Background()
	created, err := services.Session.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
		State: map[string]any{
			"hello": "world",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Session.ID() != "test-session" {
		t.Fatalf("created session id = %q, want test-session", created.Session.ID())
	}

	got, err := services.Session.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Session.ID() != "test-session" {
		t.Fatalf("got session id = %q, want test-session", got.Session.ID())
	}
	stateValue, err := got.Session.State().Get("hello")
	if err != nil {
		t.Fatalf("state Get() error = %v", err)
	}
	if stateValue != "world" {
		t.Fatalf("state hello = %v, want world", stateValue)
	}
}

func TestBuildServicesSessionDatabaseUsesSharedDatabaseConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Default(dir)
	cfg.Storage.Database = DatabaseConfig{
		Type:        "sqlite",
		DSN:         filepath.Join(dir, "platform", "adk.db"),
		AutoMigrate: true,
	}
	cfg.Storage.Session = ServiceConfig{Type: "database"}
	cfg.normalize(dir)

	services, err := cfg.BuildServices()
	if err != nil {
		t.Fatalf("BuildServices() error = %v", err)
	}

	_, err = services.Session.Create(context.Background(), &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "from-shared-db-config",
	})
	if err != nil {
		t.Fatalf("Create() with shared database config error = %v", err)
	}
}

func TestBuildServicesPostgresSessionRequiresDSN(t *testing.T) {
	t.Parallel()

	cfg := Default(t.TempDir())
	cfg.Storage.Database = DatabaseConfig{Type: "postgres"}
	cfg.Storage.Session = ServiceConfig{Type: "database"}

	_, err := cfg.BuildServices()
	if err == nil {
		t.Fatalf("BuildServices() error = nil, want postgres dsn error")
	}
}
