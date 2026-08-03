// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package envmanagertool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// BusinessLogRequest describes a guarded, read-only business log stream.
// It is intentionally narrower than free-form shell execution.
type BusinessLogRequest struct {
	EnvironmentID string `json:"environment_id"`
	Kind          string `json:"kind"` // docker|k8s|file|journal
	Container     string `json:"container,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	Pod           string `json:"pod,omitempty"`
	K8sContainer  string `json:"k8s_container,omitempty"`
	Path          string `json:"path,omitempty"`
	Unit          string `json:"unit,omitempty"`
	Tail          int    `json:"tail,omitempty"`
	Follow        bool   `json:"follow"`
}

// BusinessLogEvent is emitted as an SSE JSON payload by platform REST handlers.
type BusinessLogEvent struct {
	Type          string `json:"type"`
	StreamID      string `json:"stream_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Target        string `json:"target,omitempty"`
	Line          string `json:"line,omitempty"`
	Source        string `json:"source,omitempty"`
	Command       string `json:"command,omitempty"`
	Message       string `json:"message,omitempty"`
	Timestamp     string `json:"timestamp"`
}

// StreamBusinessLogs runs a constrained log command and emits line-oriented events.
// The command set is deliberately small: docker logs, kubectl logs, tail, journalctl.
func StreamBusinessLogs(ctx context.Context, cfg Config, req BusinessLogRequest, emit func(BusinessLogEvent) error) error {
	svc, err := newService(cfg)
	if err != nil {
		return err
	}
	env, err := svc.environment(req.EnvironmentID)
	if err != nil {
		return err
	}
	if !env.AllowExecute {
		return fmt.Errorf("environment %s does not allow execution", env.ID)
	}
	command, target, err := buildBusinessLogCommand(req, env)
	if err != nil {
		return err
	}
	streamID := fmt.Sprintf("blog_%d", time.Now().UnixNano())
	start := BusinessLogEvent{Type: "business.log.start", StreamID: streamID, EnvironmentID: env.ID, Kind: req.Kind, Target: target, Command: command, Timestamp: time.Now().Format(time.RFC3339Nano)}
	if err := emit(start); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	err = svc.streamCommand(runCtx, env, command, func(source, line string) error {
		return emit(BusinessLogEvent{Type: "business.log.line", StreamID: streamID, EnvironmentID: env.ID, Kind: req.Kind, Target: target, Source: source, Line: redactOutput(line), Timestamp: time.Now().Format(time.RFC3339Nano)})
	})
	if err != nil {
		_ = emit(BusinessLogEvent{Type: "business.log.error", StreamID: streamID, EnvironmentID: env.ID, Kind: req.Kind, Target: target, Message: err.Error(), Timestamp: time.Now().Format(time.RFC3339Nano)})
		return err
	}
	return emit(BusinessLogEvent{Type: "business.log.done", StreamID: streamID, EnvironmentID: env.ID, Kind: req.Kind, Target: target, Message: "log stream completed", Timestamp: time.Now().Format(time.RFC3339Nano)})
}

func buildBusinessLogCommand(req BusinessLogRequest, env Environment) (string, string, error) {
	tail := req.Tail
	if tail <= 0 {
		tail = 200
	}
	tail = clamp(tail, 1, 5000)
	follow := req.Follow
	switch strings.ToLower(strings.TrimSpace(req.Kind)) {
	case "docker":
		container := strings.TrimSpace(req.Container)
		if container == "" {
			return "", "", fmt.Errorf("container is required for docker logs")
		}
		if !safeNameRE.MatchString(container) {
			return "", "", fmt.Errorf("container contains unsafe characters")
		}
		args := []string{"docker", "logs", "--tail", fmt.Sprintf("%d", tail)}
		if follow {
			args = append(args, "-f")
		}
		args = append(args, shellQuote(container))
		return strings.Join(args, " "), container, nil
	case "k8s":
		ns := strings.TrimSpace(req.Namespace)
		pod := strings.TrimSpace(req.Pod)
		if ns == "" || pod == "" {
			return "", "", fmt.Errorf("namespace and pod are required for k8s logs")
		}
		if !safeK8sNameRE.MatchString(ns) || !safeK8sNameRE.MatchString(pod) {
			return "", "", fmt.Errorf("namespace or pod contains unsafe characters")
		}
		parts := []string{"kubectl", "logs", "-n", shellQuote(ns), shellQuote(pod), "--tail", fmt.Sprintf("%d", tail)}
		if c := strings.TrimSpace(req.K8sContainer); c != "" {
			if !safeK8sNameRE.MatchString(c) {
				return "", "", fmt.Errorf("container contains unsafe characters")
			}
			parts = append(parts, "-c", shellQuote(c))
		}
		if follow {
			parts = append(parts, "-f")
		}
		return strings.Join(parts, " "), ns + "/" + pod, nil
	case "file":
		path := strings.TrimSpace(req.Path)
		if path == "" {
			return "", "", fmt.Errorf("path is required for file logs")
		}
		if err := validatePath(path, env); err != nil {
			return "", "", err
		}
		cmd := fmt.Sprintf("tail -n %d", tail)
		if follow {
			cmd += " -F"
		}
		cmd += " -- " + shellQuote(path)
		return cmd, path, nil
	case "journal":
		unit := strings.TrimSpace(req.Unit)
		if unit == "" {
			return "", "", fmt.Errorf("unit is required for journal logs")
		}
		if !safeNameRE.MatchString(unit) {
			return "", "", fmt.Errorf("unit contains unsafe characters")
		}
		cmd := fmt.Sprintf("journalctl -u %s -n %d --no-pager", shellQuote(unit), tail)
		if follow {
			cmd += " -f"
		}
		return cmd, unit, nil
	default:
		return "", "", fmt.Errorf("unsupported business log kind %q", req.Kind)
	}
}

func (s *service) streamCommand(ctx context.Context, env Environment, command string, onLine func(source, line string) error) error {
	switch env.ConnectionType {
	case "local":
		if !s.cfg.AllowLocal {
			return fmt.Errorf("local execution is disabled")
		}
		cmd := exec.CommandContext(ctx, "sh", "-lc", command)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		err = scanPipes(ctx, stdout, stderr, onLine)
		waitErr := cmd.Wait()
		if err != nil {
			return err
		}
		return waitErr
	case "ssh":
		return s.streamSSHCommand(ctx, env, command, onLine)
	default:
		return fmt.Errorf("unsupported connection_type %q", env.ConnectionType)
	}
}

func (s *service) streamSSHCommand(ctx context.Context, env Environment, command string, onLine func(source, line string) error) error {
	auth, err := sshAuthMethods(env)
	if err != nil {
		return err
	}
	port := env.Port
	if port == 0 {
		port = 22
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", env.Host, port), &ssh.ClientConfig{User: env.Username, Auth: auth, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(command); err != nil {
		return err
	}
	waitDone := make(chan error, 1)
	scanDone := make(chan error, 1)
	go func() { waitDone <- sess.Wait() }()
	go func() { scanDone <- scanPipes(ctx, stdout, stderr, onLine) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return ctx.Err()
	case scanErr := <-scanDone:
		select {
		case waitErr := <-waitDone:
			if scanErr != nil {
				return scanErr
			}
			return waitErr
		case <-ctx.Done():
			_ = sess.Signal(ssh.SIGKILL)
			_ = sess.Close()
			return ctx.Err()
		}
	case waitErr := <-waitDone:
		select {
		case scanErr := <-scanDone:
			if scanErr != nil {
				return scanErr
			}
			return waitErr
		case <-time.After(2 * time.Second):
			return waitErr
		}
	}
}

func scanPipes(ctx context.Context, stdout io.Reader, stderr io.Reader, onLine func(source, line string) error) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	scan := func(source string, r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		buf := make([]byte, 0, 64*1024)
		s.Buffer(buf, 1024*1024)
		for s.Scan() {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}
			if err := onLine(source, s.Text()); err != nil {
				errCh <- err
				return
			}
		}
		if err := s.Err(); err != nil {
			errCh <- err
		}
	}
	wg.Add(2)
	go scan("stdout", stdout)
	go scan("stderr", stderr)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func EncodeBusinessLogEvent(event BusinessLogEvent) (string, error) {
	b, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
