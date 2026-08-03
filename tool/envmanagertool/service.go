// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envmanagertool

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type service struct {
	cfg          Config
	environments map[string]Environment
	operations   map[string]Operation
}

func newService(cfg Config) (*service, error) {
	base := defaultConfig()
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = base.ConfigPath
	}
	if cfg.DefaultSafetyMode == "" {
		cfg.DefaultSafetyMode = base.DefaultSafetyMode
	}
	if cfg.DefaultFreedomLevel == "" {
		cfg.DefaultFreedomLevel = base.DefaultFreedomLevel
	}
	if cfg.DefaultMaxOutputBytes == 0 {
		cfg.DefaultMaxOutputBytes = base.DefaultMaxOutputBytes
	}
	if cfg.DefaultTimeoutSeconds == 0 {
		cfg.DefaultTimeoutSeconds = base.DefaultTimeoutSeconds
	}
	if !cfg.AllowLocal {
		cfg.AllowLocal = base.AllowLocal
	}
	// DryRunDefault intentionally remains caller controlled. The default is true
	// when no config file is provided, and false may be set explicitly in YAML.
	if cfg.ConfigPath == "" {
		cfg.DryRunDefault = true
	}

	s := &service{
		cfg:          cfg,
		environments: map[string]Environment{},
		operations:   standardOperations(),
	}
	if cfg.ConfigPath != "" {
		b, err := os.ReadFile(cfg.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load environment config %s: %w", cfg.ConfigPath, err)
		}
		var cf ConfigFile
		if err := json.Unmarshal(b, &cf); err != nil {
			return nil, fmt.Errorf("parse environment config %s: %w", cfg.ConfigPath, err)
		}
		for _, env := range cf.Environments {
			if strings.TrimSpace(env.ID) == "" {
				return nil, fmt.Errorf("environment_id is required")
			}
			env = s.withDefaults(env)
			s.environments[env.ID] = env
		}
	}
	return s, nil
}

func (s *service) withDefaults(env Environment) Environment {
	if env.SafetyMode == "" {
		env.SafetyMode = s.cfg.DefaultSafetyMode
	}
	if env.FreedomLevel == "" {
		env.FreedomLevel = s.cfg.DefaultFreedomLevel
	}
	if env.Port == 0 && env.ConnectionType == "ssh" {
		env.Port = 22
	}
	if env.Name == "" {
		env.Name = env.ID
	}
	if env.Type == "" {
		env.Type = "linux"
	}
	return env
}

func (s *service) listEnvironments() []Environment {
	out := make([]Environment, 0, len(s.environments))
	for _, env := range s.environments {
		safe := env
		safe.KeyPath = redactPath(safe.KeyPath)
		out = append(out, safe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *service) environment(id string) (Environment, error) {
	if id == "" && len(s.environments) == 1 {
		for _, env := range s.environments {
			return env, nil
		}
	}
	env, ok := s.environments[id]
	if !ok {
		return Environment{}, fmt.Errorf("environment %q not found", id)
	}
	return s.withDefaults(env), nil
}

func (s *service) operation(id string) (Operation, error) {
	op, ok := s.operations[id]
	if !ok {
		return Operation{}, fmt.Errorf("operation %q not found", id)
	}
	return op, nil
}

func (s *service) buildStandardPlan(environmentID, operationID string, params map[string]any) (plan CommandPlan, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("build operation plan: %v", r)
		}
	}()
	env, err := s.environment(environmentID)
	if err != nil {
		return CommandPlan{}, err
	}
	op, err := s.operation(operationID)
	if err != nil {
		return CommandPlan{}, err
	}
	if !hasCapability(env, op.Capability) {
		return CommandPlan{}, fmt.Errorf("environment %s does not declare capability %s", env.ID, op.Capability)
	}
	plan, err = buildOperationCommand(op, params, env)
	if err != nil {
		return CommandPlan{}, err
	}
	plan.TimeoutSeconds = op.DefaultTimeoutSecs
	if plan.TimeoutSeconds <= 0 {
		plan.TimeoutSeconds = s.cfg.DefaultTimeoutSeconds
	}
	plan.MaxOutputBytes = s.cfg.DefaultMaxOutputBytes
	plan.AnalyzedAt = time.Now()
	plan.RequiresApproval = op.RequiresApproval
	plan = applyPolicy(env, s.cfg, plan, false)
	return plan, nil
}

func (s *service) buildGuardedPlan(environmentID, command, purpose, expected string) (CommandPlan, error) {
	env, err := s.environment(environmentID)
	if err != nil {
		return CommandPlan{}, err
	}
	return newCommandPlan(env, s.cfg, command, purpose, expected, true), nil
}

func (s *service) execute(ctx context.Context, env Environment, plan CommandPlan, approved bool) ExecutionResult {
	started := time.Now()
	result := ExecutionResult{
		Status:    "blocked",
		Plan:      plan,
		DryRun:    s.cfg.DryRunDefault || !env.AllowExecute,
		StartedAt: started,
	}
	if plan.Blocked {
		result.Status = "blocked"
		result.FinishedAt = time.Now()
		result.Audit = s.audit(plan, approved, result.DryRun, result.Status, 0, started, result.FinishedAt)
		return result
	}
	if plan.RequiresApproval && !approved {
		result.Status = "approval_required"
		result.FinishedAt = time.Now()
		result.Audit = s.audit(plan, approved, result.DryRun, result.Status, 0, started, result.FinishedAt)
		return result
	}
	if result.DryRun {
		result.Status = "dry_run"
		result.Output = "execution skipped: environment allow_execute=false or toolset dry_run_default=true; command preview only"
		result.FinishedAt = time.Now()
		result.Audit = s.audit(plan, approved, true, result.Status, len(result.Output), started, result.FinishedAt)
		return result
	}
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	out, exitCode, err := s.runCommand(cmdCtx, env, plan.Command)
	if err != nil {
		result.ExitCode = exitCode
	}
	output := redactOutput(string(out))
	if len(output) > plan.MaxOutputBytes {
		output = output[:plan.MaxOutputBytes] + "\n...[truncated by env tool output policy]"
		result.OutputTruncated = true
	}
	if err != nil && strings.TrimSpace(output) == "" {
		output = err.Error()
	}
	result.Output = output
	if err != nil {
		result.Status = "failed"
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
	} else {
		result.Status = "success"
		result.ExitCode = 0
	}
	result.FinishedAt = time.Now()
	result.Audit = s.audit(plan, approved, false, result.Status, len(result.Output), started, result.FinishedAt)
	return result
}

func (s *service) runCommand(ctx context.Context, env Environment, command string) ([]byte, int, error) {
	switch env.ConnectionType {
	case "local":
		if !s.cfg.AllowLocal {
			return nil, -1, fmt.Errorf("local execution is disabled; set allow_local=true only for trusted development use")
		}
		cmd := exec.CommandContext(ctx, "sh", "-lc", command)
		out, err := cmd.CombinedOutput()
		return out, exitCode(err), err
	case "ssh":
		if env.Host == "" || env.Username == "" {
			return nil, -1, fmt.Errorf("ssh environment requires host and username")
		}
		return s.runSSHCommand(ctx, env, command)
	default:
		return nil, -1, fmt.Errorf("unsupported connection_type %q", env.ConnectionType)
	}
}

func (s *service) runSSHCommand(ctx context.Context, env Environment, command string) ([]byte, int, error) {
	auth, err := sshAuthMethods(env)
	if err != nil {
		return nil, -1, err
	}
	port := env.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(env.Host, fmt.Sprintf("%d", port))
	cfg := &ssh.ClientConfig{
		User:            env.Username,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         s.timeoutDuration(),
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, -1, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, -1, err
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, -1, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return append(stdout.Bytes(), stderr.Bytes()...), -1, ctx.Err()
	case err := <-done:
		out := append(stdout.Bytes(), stderr.Bytes()...)
		return out, exitCode(err), err
	}
}

func sshAuthMethods(env Environment) ([]ssh.AuthMethod, error) {
	auth := []ssh.AuthMethod{}
	password := ""
	if env.PasswordEnv != "" {
		password = os.Getenv(env.PasswordEnv)
		if password == "" {
			return nil, fmt.Errorf("ssh environment %s password_env %q is not set", env.ID, env.PasswordEnv)
		}
		auth = append(auth, ssh.Password(password))
	}
	if env.KeyPath != "" {
		key, err := os.ReadFile(env.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read ssh key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil && password != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(password))
		}
		if err != nil {
			return nil, fmt.Errorf("parse ssh key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("ssh environment requires key_path or password_env")
	}
	return auth, nil
}

func (s *service) timeoutDuration() time.Duration {
	if s.cfg.DefaultTimeoutSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(s.cfg.DefaultTimeoutSeconds) * time.Second
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var execExit *exec.ExitError
	if errors.As(err, &execExit) {
		return execExit.ExitCode()
	}
	var sshExit *ssh.ExitError
	if errors.As(err, &sshExit) {
		return sshExit.ExitStatus()
	}
	return -1
}

func (s *service) audit(plan CommandPlan, approved, dryRun bool, status string, outputBytes int, started, finished time.Time) AuditRecord {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%s|%d", plan.EnvironmentID, plan.OperationID, plan.Command, started.UnixNano())))
	return AuditRecord{
		AuditID:       "env_audit_" + hex.EncodeToString(h[:])[:16],
		EnvironmentID: plan.EnvironmentID,
		OperationID:   plan.OperationID,
		RiskLevel:     plan.RiskLevel,
		Approved:      approved,
		DryRun:        dryRun,
		StartedAt:     started,
		FinishedAt:    finished,
		Status:        status,
		OutputBytes:   outputBytes,
	}
}

func hasCapability(env Environment, cap string) bool {
	if cap == "" {
		return true
	}
	for _, c := range env.Capabilities {
		if c == cap {
			return true
		}
		if strings.HasSuffix(cap, ".read") && c == strings.TrimSuffix(cap, ".read")+".*" {
			return true
		}
		if strings.HasSuffix(cap, ".write") && c == strings.TrimSuffix(cap, ".write")+".*" {
			return true
		}
	}
	return false
}

func redactPath(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return "***"
	}
	return strings.Join(parts[:len(parts)-1], "/") + "/***"
}

func redactOutput(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "authorization:") {
			lines[i] = "[redacted sensitive line]"
		}
		if strings.Contains(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY") {
			lines[i] = "[redacted private key]"
		}
	}
	return strings.Join(lines, "\n")
}
