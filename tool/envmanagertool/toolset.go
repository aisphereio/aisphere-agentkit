// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envmanagertool

import (
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const envToolInstructions = `
Environment-management tools are guarded. Prefer standard operations first. Use env_analyze_command before proposing free-form shell commands. Use env_run_guarded_command only when standard operations do not cover the task, and always provide purpose and expected_effect. Never ask for or reveal secrets. For production-like environments, summarize current environment_id, risk level, command preview, and whether approval is required before execution.
`

// NewToolset creates a guarded environment-management toolset.
func NewToolset(cfg Config) (tool.Toolset, error) {
	svc, err := newService(cfg)
	if err != nil {
		return nil, err
	}
	return &Toolset{svc: svc}, nil
}

// Toolset exposes environment-management tools to agents.
type Toolset struct{ svc *service }

func (t *Toolset) Name() string { return "EnvToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return []tool.Tool{
		&envTool{name: "env_list_environments", desc: "List configured managed environments without exposing secrets.", schema: schemaObject(map[string]*genai.Schema{}, nil), handler: t.listEnvironments},
		&envTool{name: "env_list_operations", desc: "List allowed standard environment operations. Optional category filters: linux, docker, k8s, go, python.", schema: schemaObject(map[string]*genai.Schema{"category": {Type: genai.TypeString, Description: "Optional category filter."}}, nil), handler: t.listOperations},
		&envTool{name: "env_open_business_log_panel", desc: "Prepare a structured UI response that opens the business log panel for Docker, Kubernetes, file, or journal logs.", schema: schemaObject(map[string]*genai.Schema{
			"environment_id": {Type: genai.TypeString, Description: "Target environment id."},
			"kind":           {Type: genai.TypeString, Description: "One of docker, k8s, file, journal."},
			"container":      {Type: genai.TypeString, Description: "Docker container name or ID."},
			"namespace":      {Type: genai.TypeString, Description: "Kubernetes namespace."},
			"pod":            {Type: genai.TypeString, Description: "Kubernetes pod name."},
			"k8s_container":  {Type: genai.TypeString, Description: "Optional Kubernetes container name."},
			"path":           {Type: genai.TypeString, Description: "Log file path."},
			"unit":           {Type: genai.TypeString, Description: "systemd unit name."},
			"tail":           {Type: genai.TypeInteger, Description: "Number of lines to fetch initially."},
			"follow":         {Type: genai.TypeBoolean, Description: "Whether to keep streaming new lines."},
		}, []string{"environment_id", "kind"}), handler: t.openBusinessLogPanel},
		&envTool{name: "env_analyze_command", desc: "Analyze a shell command for risk before proposing execution. This does not execute anything.", schema: schemaObject(map[string]*genai.Schema{
			"environment_id":  {Type: genai.TypeString, Description: "Target environment id."},
			"command":         {Type: genai.TypeString, Description: "Command to analyze."},
			"purpose":         {Type: genai.TypeString, Description: "Why this command is needed."},
			"expected_effect": {Type: genai.TypeString, Description: "Expected effect and whether it should be read-only."},
		}, []string{"environment_id", "command"}), handler: t.analyzeCommand},
		&envTool{name: "env_execute_operation", desc: "Execute or dry-run a platform-defined standard operation by operation_id with structured parameters. The tool enforces environment capabilities, risk policy, approval, dry-run, output limits, and audit metadata.", schema: schemaObject(map[string]*genai.Schema{
			"environment_id": {Type: genai.TypeString, Description: "Target environment id."},
			"operation_id":   {Type: genai.TypeString, Description: "Standard operation id, for example k8s.get_pod_logs or docker.ps."},
			"parameters":     {Type: genai.TypeObject, Description: "Operation parameters. Use env_list_operations to inspect expected parameters."},
		}, []string{"environment_id", "operation_id"}), handler: t.executeOperation},
		&envTool{name: "env_run_guarded_command", desc: "Propose and, after required approval, run a free-form guarded shell command. Use only when standard operations are insufficient. The tool risk-scans, requests approval, supports dry-run, truncates output, and returns audit metadata.", schema: schemaObject(map[string]*genai.Schema{
			"environment_id":  {Type: genai.TypeString, Description: "Target environment id."},
			"command":         {Type: genai.TypeString, Description: "Free-form shell command to run after policy checks and approval."},
			"purpose":         {Type: genai.TypeString, Description: "Why this command is needed."},
			"expected_effect": {Type: genai.TypeString, Description: "Expected effect, impact, and whether it is read-only."},
		}, []string{"environment_id", "command", "purpose"}), handler: t.runGuardedCommand},
	}, nil
}

type envTool struct {
	name    string
	desc    string
	schema  *genai.Schema
	handler func(tool.Context, map[string]any) (map[string]any, error)
}

func (t *envTool) Name() string        { return t.name }
func (t *envTool) Description() string { return t.desc }
func (t *envTool) IsLongRunning() bool { return false }
func (t *envTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: t.name, Description: t.desc, Parameters: t.schema}
}
func (t *envTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, envToolInstructions)
	return nil
}
func (t *envTool) Run(ctx tool.Context, args any) (result map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s failed: %v", t.name, r)
		}
	}()
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s args must be an object", t.name)
	}
	return t.handler(ctx, m)
}

func (t *Toolset) listEnvironments(ctx tool.Context, args map[string]any) (map[string]any, error) {
	return map[string]any{"environments": t.svc.listEnvironments(), "count": len(t.svc.environments)}, nil
}

func (t *Toolset) listOperations(ctx tool.Context, args map[string]any) (map[string]any, error) {
	category := strings.TrimSpace(optionalString(args, "category", ""))
	ops := sortedOperations(t.svc.operations, category)
	return map[string]any{"operations": ops, "count": len(ops), "category": category}, nil
}

func (t *Toolset) openBusinessLogPanel(ctx tool.Context, args map[string]any) (map[string]any, error) {
	environmentID := requiredString(args, "environment_id")
	env, err := t.svc.environment(environmentID)
	if err != nil {
		return nil, err
	}
	req := BusinessLogRequest{
		EnvironmentID: environmentID,
		Kind:          requiredString(args, "kind"),
		Container:     optionalString(args, "container", ""),
		Namespace:     optionalString(args, "namespace", ""),
		Pod:           optionalString(args, "pod", ""),
		K8sContainer:  optionalString(args, "k8s_container", ""),
		Path:          optionalString(args, "path", ""),
		Unit:          optionalString(args, "unit", ""),
		Tail:          optionalInt(args, "tail", 200),
		Follow:        optionalBool(args, "follow", true),
	}
	command, target, err := buildBusinessLogCommand(req, env)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":  "ready",
		"message": fmt.Sprintf("Business log panel is ready for %s", target),
		"target":  target,
		"ui": map[string]any{
			"renderer":  "business_log_panel",
			"auto_open": true,
			"request": map[string]any{
				"environment_id": req.EnvironmentID,
				"kind":           req.Kind,
				"container":      req.Container,
				"namespace":      req.Namespace,
				"pod":            req.Pod,
				"k8s_container":  req.K8sContainer,
				"path":           req.Path,
				"unit":           req.Unit,
				"tail":           req.Tail,
				"follow":         req.Follow,
				"target":         target,
				"command":        command,
			},
		},
	}, nil
}

func (t *Toolset) analyzeCommand(ctx tool.Context, args map[string]any) (map[string]any, error) {
	environmentID := requiredString(args, "environment_id")
	command := requiredString(args, "command")
	purpose := optionalString(args, "purpose", "")
	expected := optionalString(args, "expected_effect", "")
	plan, err := t.svc.buildGuardedPlan(environmentID, command, purpose, expected)
	if err != nil {
		return nil, err
	}
	return map[string]any{"plan": plan, "message": "Command analyzed only; not executed."}, nil
}

func (t *Toolset) executeOperation(ctx tool.Context, args map[string]any) (map[string]any, error) {
	environmentID := requiredString(args, "environment_id")
	operationID := requiredString(args, "operation_id")
	params, _ := args["parameters"].(map[string]any)
	plan, err := t.svc.buildStandardPlan(environmentID, operationID, params)
	if err != nil {
		return nil, err
	}
	env, err := t.svc.environment(environmentID)
	if err != nil {
		return nil, err
	}
	approved := false
	if confirmation := ctx.ToolConfirmation(); confirmation != nil {
		if !confirmation.Confirmed {
			return map[string]any{"status": "rejected", "plan": plan}, nil
		}
		approved = true
	} else if plan.RequiresApproval {
		if err := ctx.RequestConfirmation("请确认环境操作："+operationID, approvalPayload("standard_operation", plan)); err != nil {
			return nil, err
		}
		ctx.Actions().SkipSummarization = true
		return map[string]any{"status": "approval_required", "plan": plan}, fmt.Errorf("error tool %q %w", "env_execute_operation", tool.ErrConfirmationRequired)
	}
	result := t.svc.execute(ctx, env, plan, approved)
	return map[string]any{"result": result}, nil
}

func (t *Toolset) runGuardedCommand(ctx tool.Context, args map[string]any) (map[string]any, error) {
	environmentID := requiredString(args, "environment_id")
	command := requiredString(args, "command")
	purpose := requiredString(args, "purpose")
	expected := optionalString(args, "expected_effect", "")
	plan, err := t.svc.buildGuardedPlan(environmentID, command, purpose, expected)
	if err != nil {
		return nil, err
	}
	env, err := t.svc.environment(environmentID)
	if err != nil {
		return nil, err
	}
	approved := false
	if confirmation := ctx.ToolConfirmation(); confirmation != nil {
		if !confirmation.Confirmed {
			return map[string]any{"status": "rejected", "plan": plan}, nil
		}
		approved = true
	} else if plan.RequiresApproval {
		if err := ctx.RequestConfirmation("请确认自由环境命令", approvalPayload("guarded_shell", plan)); err != nil {
			return nil, err
		}
		ctx.Actions().SkipSummarization = true
		return map[string]any{"status": "approval_required", "plan": plan}, fmt.Errorf("error tool %q %w", "env_run_guarded_command", tool.ErrConfirmationRequired)
	}
	result := t.svc.execute(ctx, env, plan, approved)
	return map[string]any{"result": result}, nil
}

func approvalPayload(kind string, plan CommandPlan) map[string]any {
	return map[string]any{
		"kind":             "environment_operation_approval",
		"operation_kind":   kind,
		"environment_id":   plan.EnvironmentID,
		"operation_id":     plan.OperationID,
		"command_preview":  plan.Command,
		"purpose":          plan.Purpose,
		"expected_effect":  plan.ExpectedEffect,
		"risk_level":       plan.RiskLevel,
		"read_only":        plan.ReadOnly,
		"warnings":         plan.Warnings,
		"blocked":          plan.Blocked,
		"block_reasons":    plan.BlockReasons,
		"timeout_seconds":  plan.TimeoutSeconds,
		"max_output_bytes": plan.MaxOutputBytes,
	}
}

func schemaObject(props map[string]*genai.Schema, required []string) *genai.Schema {
	return &genai.Schema{Type: genai.TypeObject, Properties: props, Required: required}
}

var _ tool.Toolset = (*Toolset)(nil)
var _ tool.Tool = (*envTool)(nil)
