// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envmanagertool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveSSHReadOnlyOperation(t *testing.T) {
	if os.Getenv("ADK_ENVMANAGER_LIVE_TEST") != "1" {
		t.Skip("set ADK_ENVMANAGER_LIVE_TEST=1 to run live SSH integration test")
	}
	if os.Getenv("ADK_ENV_HONGMEI_ROOT_PASSWORD") == "" {
		t.Skip("set ADK_ENV_HONGMEI_ROOT_PASSWORD to run live SSH integration test")
	}
	svc, err := newService(Config{DryRunDefault: false, DefaultTimeoutSeconds: 15})
	if err != nil {
		t.Fatal(err)
	}
	svc.cfg.DryRunDefault = false
	env := svc.withDefaults(Environment{
		ID:             "hongmei-root-ssh",
		ConnectionType: "ssh",
		Host:           "CHANGE_ME_HOST",
		Port:           22,
		Username:       "root",
		PasswordEnv:    "ADK_ENV_HONGMEI_ROOT_PASSWORD",
		FreedomLevel:   FreedomF3,
		SafetyMode:     SafetyModeSafeApproval,
		Capabilities:   []string{"linux.read"},
		AllowExecute:   true,
	})
	svc.environments[env.ID] = env
	plan, err := svc.buildStandardPlan(env.ID, "linux.pwd", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result := svc.execute(ctx, env, plan, false)
	if result.Status != "success" {
		t.Fatalf("status=%s exit=%d output=%q", result.Status, result.ExitCode, result.Output)
	}
	if !strings.Contains(strings.TrimSpace(result.Output), "/") {
		t.Fatalf("unexpected pwd output: %q", result.Output)
	}
}
