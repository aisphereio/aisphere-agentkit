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
	"regexp"
	"sort"
	"strings"
)

var safeNameRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
var safeK8sNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func standardOperations() map[string]Operation {
	ops := []Operation{
		{ID: "linux.pwd", Name: "查看当前目录", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "执行 pwd 查看当前工作目录。", DefaultTimeoutSecs: 10},
		{ID: "linux.ls", Name: "列目录", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "列出指定目录内容。", Parameters: map[string]string{"path": "目录路径，默认 ."}, DefaultTimeoutSecs: 10},
		{ID: "linux.tail_file", Name: "查看文件尾部", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "tail 指定文件，适合查看日志。", Parameters: map[string]string{"path": "文件路径", "lines": "行数，默认 200"}, DefaultTimeoutSecs: 20},
		{ID: "linux.df", Name: "查看磁盘", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "执行 df -h。", DefaultTimeoutSecs: 10},
		{ID: "linux.free", Name: "查看内存", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "执行 free -m。", DefaultTimeoutSecs: 10},
		{ID: "linux.ps", Name: "查看进程", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "执行 ps aux 的截断视图。", DefaultTimeoutSecs: 10},
		{ID: "linux.ss_listen", Name: "查看监听端口", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "执行 ss -lntup。", DefaultTimeoutSecs: 10},
		{ID: "linux.systemctl_status", Name: "查看 systemd 服务状态", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "只读查看 systemctl status。", Parameters: map[string]string{"unit": "服务名"}, DefaultTimeoutSecs: 15},
		{ID: "linux.journalctl_tail", Name: "查看服务日志", Category: "linux", Capability: "linux.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "只读查看 journalctl 末尾日志。", Parameters: map[string]string{"unit": "服务名", "lines": "行数，默认 200"}, DefaultTimeoutSecs: 20},

		{ID: "docker.ps", Name: "列 Docker 容器", Category: "docker", Capability: "docker.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "执行 docker ps -a。", DefaultTimeoutSecs: 15},
		{ID: "docker.logs", Name: "看 Docker 容器日志", Category: "docker", Capability: "docker.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "查看指定容器日志。", Parameters: map[string]string{"container": "容器名或 ID", "lines": "行数，默认 200"}, DefaultTimeoutSecs: 30},
		{ID: "docker.inspect_container", Name: "检查 Docker 容器", Category: "docker", Capability: "docker.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "docker inspect 指定容器。", Parameters: map[string]string{"container": "容器名或 ID"}, DefaultTimeoutSecs: 20},
		{ID: "docker.restart_container", Name: "重启 Docker 容器", Category: "docker", Capability: "docker.write", RiskLevel: RiskL3, FreedomLevel: FreedomF4, Description: "重启指定容器，必须审批。", Parameters: map[string]string{"container": "容器名或 ID"}, RequiresApproval: true, DefaultTimeoutSecs: 60},

		{ID: "k8s.list_namespaces", Name: "列 K8s 命名空间", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "kubectl get namespaces。", DefaultTimeoutSecs: 15},
		{ID: "k8s.list_pods", Name: "列 Pod", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "kubectl get pods。", Parameters: map[string]string{"namespace": "命名空间，可空表示全部"}, DefaultTimeoutSecs: 20},
		{ID: "k8s.describe_pod", Name: "Describe Pod", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "kubectl describe pod。", Parameters: map[string]string{"namespace": "命名空间", "pod": "Pod 名"}, DefaultTimeoutSecs: 30},
		{ID: "k8s.get_pod_logs", Name: "查看 Pod 日志", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "kubectl logs 指定 Pod。", Parameters: map[string]string{"namespace": "命名空间", "pod": "Pod 名", "container": "容器名，可空", "lines": "行数，默认 200", "previous": "是否查看上一次崩溃日志"}, DefaultTimeoutSecs: 30},
		{ID: "k8s.list_events", Name: "列 K8s 事件", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "kubectl get events。", Parameters: map[string]string{"namespace": "命名空间，可空表示全部"}, DefaultTimeoutSecs: 30},
		{ID: "k8s.list_services", Name: "列 Service", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "kubectl get svc。", Parameters: map[string]string{"namespace": "命名空间，可空表示全部"}, DefaultTimeoutSecs: 20},
		{ID: "k8s.list_deployments", Name: "列 Deployment", Category: "k8s", Capability: "k8s.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "kubectl get deploy。", Parameters: map[string]string{"namespace": "命名空间，可空表示全部"}, DefaultTimeoutSecs: 20},
		{ID: "k8s.rollout_restart_deployment", Name: "重启 Deployment", Category: "k8s", Capability: "k8s.write", RiskLevel: RiskL3, FreedomLevel: FreedomF4, Description: "kubectl rollout restart deployment，必须审批。", Parameters: map[string]string{"namespace": "命名空间", "deployment": "Deployment 名"}, RequiresApproval: true, DefaultTimeoutSecs: 120},

		{ID: "go.version", Name: "Go 版本", Category: "go", Capability: "go.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "go version。", DefaultTimeoutSecs: 10},
		{ID: "go.env", Name: "Go 环境", Category: "go", Capability: "go.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "go env。", DefaultTimeoutSecs: 15},
		{ID: "python.version", Name: "Python 版本", Category: "python", Capability: "python.read", RiskLevel: RiskL1, FreedomLevel: FreedomF1, Description: "python3 --version 或 python --version。", DefaultTimeoutSecs: 10},
		{ID: "python.pip_list", Name: "Python pip list", Category: "python", Capability: "python.read", RiskLevel: RiskL1, FreedomLevel: FreedomF2, Description: "python3 -m pip list。", DefaultTimeoutSecs: 30},
	}
	m := make(map[string]Operation, len(ops))
	for _, op := range ops {
		if op.DefaultTimeoutSecs == 0 {
			op.DefaultTimeoutSecs = 30
		}
		m[op.ID] = op
	}
	return m
}

func sortedOperations(ops map[string]Operation, category string) []Operation {
	out := make([]Operation, 0, len(ops))
	for _, op := range ops {
		if category == "" || strings.EqualFold(op.Category, category) {
			out = append(out, op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func buildOperationCommand(op Operation, params map[string]any, env Environment) (CommandPlan, error) {
	cmd := ""
	purpose := op.Description
	warnings := []string{}
	switch op.ID {
	case "linux.pwd":
		cmd = "pwd"
	case "linux.ls":
		path := optionalString(params, "path", ".")
		if err := validatePath(path, env); err != nil {
			return CommandPlan{}, err
		}
		cmd = "ls -lah -- " + shellQuote(path)
	case "linux.tail_file":
		path := requiredString(params, "path")
		if err := validatePath(path, env); err != nil {
			return CommandPlan{}, err
		}
		lines := optionalInt(params, "lines", 200)
		cmd = fmt.Sprintf("tail -n %d -- %s", clamp(lines, 1, 2000), shellQuote(path))
	case "linux.df":
		cmd = "df -h"
	case "linux.free":
		cmd = "free -m"
	case "linux.ps":
		cmd = "ps aux | head -200"
	case "linux.ss_listen":
		cmd = "ss -lntup"
	case "linux.systemctl_status":
		unit := requiredSafeName(params, "unit")
		cmd = "systemctl status --no-pager -- " + shellQuote(unit)
	case "linux.journalctl_tail":
		unit := requiredSafeName(params, "unit")
		lines := optionalInt(params, "lines", 200)
		cmd = fmt.Sprintf("journalctl -u %s -n %d --no-pager", shellQuote(unit), clamp(lines, 1, 2000))
	case "docker.ps":
		cmd = "docker ps -a --no-trunc"
	case "docker.logs":
		container := requiredSafeName(params, "container")
		lines := optionalInt(params, "lines", 200)
		cmd = fmt.Sprintf("docker logs --tail %d %s", clamp(lines, 1, 2000), shellQuote(container))
	case "docker.inspect_container":
		container := requiredSafeName(params, "container")
		cmd = "docker inspect " + shellQuote(container)
	case "docker.restart_container":
		container := requiredSafeName(params, "container")
		cmd = "docker restart " + shellQuote(container)
	case "k8s.list_namespaces":
		cmd = "kubectl get namespaces"
	case "k8s.list_pods":
		ns := optionalK8sName(params, "namespace", "")
		if ns == "" {
			cmd = "kubectl get pods -A -o wide"
		} else {
			cmd = "kubectl get pods -n " + shellQuote(ns) + " -o wide"
		}
	case "k8s.describe_pod":
		ns := requiredK8sName(params, "namespace")
		pod := requiredK8sName(params, "pod")
		cmd = fmt.Sprintf("kubectl describe pod %s -n %s", shellQuote(pod), shellQuote(ns))
	case "k8s.get_pod_logs":
		ns := requiredK8sName(params, "namespace")
		pod := requiredK8sName(params, "pod")
		container := optionalK8sName(params, "container", "")
		lines := optionalInt(params, "lines", 200)
		previous := optionalBool(params, "previous", false)
		parts := []string{"kubectl", "logs", "-n", shellQuote(ns), shellQuote(pod), "--tail", fmt.Sprintf("%d", clamp(lines, 1, 5000))}
		if container != "" {
			parts = append(parts, "-c", shellQuote(container))
		}
		if previous {
			parts = append(parts, "--previous")
			warnings = append(warnings, "previous=true 会读取上一次容器实例日志，通常用于 CrashLoopBackOff 排查。")
		}
		cmd = strings.Join(parts, " ")
	case "k8s.list_events":
		ns := optionalK8sName(params, "namespace", "")
		if ns == "" {
			cmd = "kubectl get events -A --sort-by=.lastTimestamp"
		} else {
			cmd = "kubectl get events -n " + shellQuote(ns) + " --sort-by=.lastTimestamp"
		}
	case "k8s.list_services":
		ns := optionalK8sName(params, "namespace", "")
		if ns == "" {
			cmd = "kubectl get svc -A -o wide"
		} else {
			cmd = "kubectl get svc -n " + shellQuote(ns) + " -o wide"
		}
	case "k8s.list_deployments":
		ns := optionalK8sName(params, "namespace", "")
		if ns == "" {
			cmd = "kubectl get deploy -A -o wide"
		} else {
			cmd = "kubectl get deploy -n " + shellQuote(ns) + " -o wide"
		}
	case "k8s.rollout_restart_deployment":
		ns := requiredK8sName(params, "namespace")
		deploy := requiredK8sName(params, "deployment")
		cmd = fmt.Sprintf("kubectl rollout restart deployment/%s -n %s", shellQuote(deploy), shellQuote(ns))
	case "go.version":
		cmd = "go version"
	case "go.env":
		cmd = "go env"
	case "python.version":
		cmd = "python3 --version || python --version"
	case "python.pip_list":
		cmd = "python3 -m pip list || python -m pip list"
	default:
		return CommandPlan{}, fmt.Errorf("unsupported operation %q", op.ID)
	}
	return CommandPlan{
		EnvironmentID:  env.ID,
		OperationID:    op.ID,
		Command:        cmd,
		Purpose:        purpose,
		ExpectedEffect: "平台生成的受控操作命令；按风险策略决定是否需要审批。",
		RiskLevel:      op.RiskLevel,
		ReadOnly:       riskRank(op.RiskLevel) <= riskRank(RiskL1),
		Parameters:     params,
		Warnings:       warnings,
	}, nil
}

func requiredString(params map[string]any, key string) string {
	v := strings.TrimSpace(optionalString(params, key, ""))
	if v == "" {
		panic(fmt.Sprintf("missing required parameter %q", key))
	}
	return v
}

func optionalString(params map[string]any, key, fallback string) string {
	if params == nil {
		return fallback
	}
	v, ok := params[key]
	if !ok || v == nil {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return strings.TrimSpace(s)
}

func optionalBool(params map[string]any, key string, fallback bool) bool {
	if params == nil {
		return fallback
	}
	v, ok := params[key]
	if !ok || v == nil {
		return fallback
	}
	b, ok := v.(bool)
	if !ok {
		return fallback
	}
	return b
}

func optionalInt(params map[string]any, key string, fallback int) int {
	if params == nil {
		return fallback
	}
	v, ok := params[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return fallback
	}
}

func requiredSafeName(params map[string]any, key string) string {
	v := requiredString(params, key)
	if !safeNameRE.MatchString(v) {
		panic(fmt.Sprintf("parameter %q contains unsafe characters", key))
	}
	return v
}

func requiredK8sName(params map[string]any, key string) string {
	v := requiredString(params, key)
	if !safeK8sNameRE.MatchString(v) {
		panic(fmt.Sprintf("parameter %q contains unsafe Kubernetes name characters", key))
	}
	return v
}

func optionalK8sName(params map[string]any, key, fallback string) string {
	v := optionalString(params, key, fallback)
	if v == "" {
		return ""
	}
	if !safeK8sNameRE.MatchString(v) {
		panic(fmt.Sprintf("parameter %q contains unsafe Kubernetes name characters", key))
	}
	return v
}

func validatePath(path string, env Environment) error {
	p := strings.TrimSpace(path)
	if p == "" || strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("invalid path")
	}
	if isSensitivePath(p) {
		return fmt.Errorf("refuse to access sensitive path %q", p)
	}
	if len(env.AllowedPaths) == 0 || p == "." {
		return nil
	}
	for _, allowed := range env.AllowedPaths {
		allowed = strings.TrimRight(allowed, "/")
		if allowed != "" && (p == allowed || strings.HasPrefix(p, allowed+"/")) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside allowed paths", p)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
