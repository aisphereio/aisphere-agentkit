// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envmanagertool

import "testing"

func TestAnalyzeCommand_ReadOnlyK8sCommand(t *testing.T) {
	finding := analyzeCommand("kubectl get pods -A -o wide | grep CrashLoopBackOff")
	if finding.Blocked {
		t.Fatalf("read-only k8s query should not be blocked: %#v", finding)
	}
	if finding.Risk != RiskL1 {
		t.Fatalf("risk = %s, want %s", finding.Risk, RiskL1)
	}
	if !finding.ReadOnly {
		t.Fatalf("expected read-only command")
	}
}

func TestSSHAuthRequiresCredentialReference(t *testing.T) {
	_, err := sshAuthMethods(Environment{
		ID:             "ssh-dev",
		ConnectionType: "ssh",
		Host:           "127.0.0.1",
		Username:       "root",
	})
	if err == nil {
		t.Fatalf("expected missing ssh credential error")
	}
}

func TestSSHAuthUsesPasswordEnvWithoutExposingSecret(t *testing.T) {
	t.Setenv("ADK_TEST_ENV_PASSWORD", "unit-test-secret")
	auth, err := sshAuthMethods(Environment{
		ID:             "ssh-dev",
		ConnectionType: "ssh",
		Host:           "127.0.0.1",
		Username:       "root",
		PasswordEnv:    "ADK_TEST_ENV_PASSWORD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(auth) == 0 {
		t.Fatalf("expected password auth method")
	}
}

func TestAnalyzeCommand_BlocksDestructiveCommand(t *testing.T) {
	finding := analyzeCommand("rm -rf /var/lib/mysql")
	if !finding.Blocked {
		t.Fatalf("destructive command should be blocked: %#v", finding)
	}
	if finding.Risk != RiskL4 {
		t.Fatalf("risk = %s, want %s", finding.Risk, RiskL4)
	}
}

func TestBuildStandardPlanK8sLogs(t *testing.T) {
	svc, err := newService(Config{DefaultSafetyMode: SafetyModeSafeApproval, DefaultFreedomLevel: FreedomF2, DryRunDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.environments["dev"] = Environment{
		ID:             "dev",
		ConnectionType: "local",
		SafetyMode:     SafetyModeSafeApproval,
		FreedomLevel:   FreedomF2,
		Capabilities:   []string{"k8s.read"},
		AllowExecute:   false,
	}
	plan, err := svc.buildStandardPlan("dev", "k8s.get_pod_logs", map[string]any{
		"namespace": "aict",
		"pod":       "redis-0",
		"lines":     100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequiresApproval {
		t.Fatalf("L1 standard logs should not require approval in safe_approval mode: %#v", plan)
	}
	if plan.Command == "" || plan.RiskLevel != RiskL1 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}
