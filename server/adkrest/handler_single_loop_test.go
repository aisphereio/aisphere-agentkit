package adkrest

import (
	"strings"
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestNewNativeSessionManagerRejectsLegacySessionWorkerLoop(t *testing.T) {
	cfg := &runtimeconfig.Config{}
	cfg.Skills.AIHub.Sandbox.Enabled = true
	cfg.Skills.AIHub.Sandbox.NativeSession = true
	cfg.Skills.AIHub.Sandbox.AdapterEndpoint = "http://sandbox-manager"
	cfg.Skills.AIHub.Sandbox.GoRunner = false

	manager, err := newNativeSessionManager(cfg)
	if err == nil {
		t.Fatal("newNativeSessionManager() error = nil, want legacy worker loop rejection")
	}
	if manager != nil {
		t.Fatalf("newNativeSessionManager() manager = %+v, want nil", manager)
	}
	if !strings.Contains(err.Error(), "go_runner must be true") {
		t.Fatalf("newNativeSessionManager() error = %q, want go_runner migration guidance", err)
	}
}

func TestNewNativeSessionManagerUsesADKGoRunner(t *testing.T) {
	cfg := &runtimeconfig.Config{}
	cfg.Skills.AIHub.Sandbox.Enabled = true
	cfg.Skills.AIHub.Sandbox.NativeSession = true
	cfg.Skills.AIHub.Sandbox.AdapterEndpoint = "http://sandbox-manager"
	cfg.Skills.AIHub.Sandbox.GoRunner = true

	manager, err := newNativeSessionManager(cfg)
	if err != nil {
		t.Fatalf("newNativeSessionManager() error = %v", err)
	}
	if manager == nil {
		t.Fatal("newNativeSessionManager() manager = nil")
	}
	if !manager.GoRunner {
		t.Fatal("newNativeSessionManager() did not enable the ADK-Go runtime loop")
	}
}
