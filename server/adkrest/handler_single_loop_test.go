package adkrest

import (
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestNewNativeSessionManagerUsesRuntimeLoopWithLegacyFlagValues(t *testing.T) {
	for _, value := range []bool{false, true} {
		cfg := &runtimeconfig.Config{}
		cfg.Skills.AIHub.Sandbox.Enabled = true
		cfg.Skills.AIHub.Sandbox.NativeSession = true
		cfg.Skills.AIHub.Sandbox.AdapterEndpoint = "http://sandbox-manager"
		cfg.Skills.AIHub.Sandbox.GoRunner = value

		manager, err := newNativeSessionManager(cfg)
		if err != nil {
			t.Fatalf("newNativeSessionManager() error = %v", err)
		}
		if manager == nil {
			t.Fatal("newNativeSessionManager() manager = nil")
		}
		if !manager.Enabled() {
			t.Fatal("newNativeSessionManager() sandbox manager is disabled")
		}
	}
}
