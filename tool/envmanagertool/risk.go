// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envmanagertool

import (
	"regexp"
	"strings"
	"time"
)

type riskFinding struct {
	Risk         RiskLevel `json:"risk_level"`
	ReadOnly     bool      `json:"read_only"`
	Blocked      bool      `json:"blocked"`
	Warnings     []string  `json:"warnings,omitempty"`
	BlockReasons []string  `json:"block_reasons,omitempty"`
}

var destructivePatterns = []string{
	`\brm\s+(-[^\n;|&]*[rf][^\n;|&]*|-[^\n;|&]*[fr][^\n;|&]*)`,
	`\bmkfs\b`, `\bdd\s+`, `\bshutdown\b`, `\breboot\b`, `\bpoweroff\b`, `\bhalt\b`,
	`\bkubectl\s+delete\b`, `\bkubectl\s+apply\b`, `\bkubectl\s+patch\b`, `\bkubectl\s+edit\b`, `\bkubectl\s+replace\b`,
	`\bdocker\s+rm\b`, `\bdocker\s+rmi\b`, `\bdocker\s+system\s+prune\b`, `\bdocker\s+volume\s+rm\b`,
	`\biptables\b`, `\bnft\b`, `\bfirewall-cmd\b`,
}

var changePatterns = []string{
	`\bsudo\b`, `\bsu\s+-`, `\bchmod\b`, `\bchown\b`, `\bpasswd\b`, `\bvisudo\b`, `\bcrontab\b`,
	`\bsystemctl\s+(restart|stop|start|reload|enable|disable)\b`, `\bservice\s+\S+\s+(restart|stop|start|reload)\b`,
	`\bdocker\s+(restart|stop|start|kill|compose\s+up|compose\s+down|compose\s+restart)\b`,
	`\bkubectl\s+(exec|rollout\s+restart|scale|cordon|drain|uncordon)\b`,
	`\b(apt|apt-get|yum|dnf|apk|pacman)\s+(install|remove|upgrade|update)\b`,
	`\bpip\s+install\b`, `\bpython\s+-m\s+pip\s+install\b`, `\bpython3\s+-m\s+pip\s+install\b`,
	`\bgo\s+install\b`, `\bgo\s+generate\b`, `\bgo\s+test\b`, `\bgo\s+build\b`,
	`\bcurl\b[^\n;|&]*\|\s*(sh|bash)\b`, `\bwget\b[^\n;|&]*\|\s*(sh|bash)\b`,
}

var likelyReadOnlyPrefixes = []string{
	"pwd", "ls", "cat", "tail", "head", "grep", "find", "du", "df", "free", "ps", "ss", "netstat",
	"journalctl", "systemctl status", "docker ps", "docker logs", "docker inspect", "docker images", "docker stats",
	"kubectl get", "kubectl describe", "kubectl logs", "kubectl top", "go version", "go env",
	"python --version", "python3 --version", "python -m pip list", "python3 -m pip list", "pip list",
}

var sensitivePathHints = []string{
	"/etc/shadow", "/etc/sudoers", "/root/.ssh", "~/.ssh", "id_rsa", "id_ed25519",
	"kubeconfig", ".kube/config", ".env", "BEGIN PRIVATE KEY", "authorized_keys",
}

func analyzeCommand(command string) riskFinding {
	cmd := strings.TrimSpace(command)
	finding := riskFinding{Risk: RiskL1, ReadOnly: true}
	if cmd == "" {
		finding.Blocked = true
		finding.BlockReasons = append(finding.BlockReasons, "command is empty")
		return finding
	}
	lower := strings.ToLower(cmd)
	for _, hint := range sensitivePathHints {
		if strings.Contains(lower, strings.ToLower(hint)) {
			finding.Warnings = append(finding.Warnings, "命令可能访问敏感路径或密钥文件："+hint)
			finding.Risk = maxRisk(finding.Risk, RiskL3)
		}
	}
	for _, pattern := range destructivePatterns {
		if regexp.MustCompile(pattern).MatchString(lower) {
			finding.Blocked = true
			finding.BlockReasons = append(finding.BlockReasons, "命中破坏性命令模式："+pattern)
			finding.Risk = maxRisk(finding.Risk, RiskL4)
			finding.ReadOnly = false
		}
	}
	for _, pattern := range changePatterns {
		if regexp.MustCompile(pattern).MatchString(lower) {
			finding.Warnings = append(finding.Warnings, "命中变更/提权/执行代码模式："+pattern)
			finding.Risk = maxRisk(finding.Risk, RiskL3)
			finding.ReadOnly = false
		}
	}
	if strings.Contains(lower, ">") || strings.Contains(lower, " >>") || strings.Contains(lower, " tee ") {
		finding.Warnings = append(finding.Warnings, "命令包含重定向或 tee，可能写文件。")
		finding.Risk = maxRisk(finding.Risk, RiskL2)
		finding.ReadOnly = false
	}
	if strings.Contains(lower, "|") && finding.Risk == RiskL1 {
		finding.Warnings = append(finding.Warnings, "命令包含管道；按只读诊断处理，但仍建议在生产环境审批。")
	}
	if finding.Risk == RiskL1 && !hasReadOnlyPrefix(lower) {
		finding.Warnings = append(finding.Warnings, "命令不属于明确只读白名单；建议作为自由命令审批执行。")
		finding.Risk = maxRisk(finding.Risk, RiskL2)
	}
	return finding
}

func hasReadOnlyPrefix(lower string) bool {
	lower = strings.TrimSpace(lower)
	for _, prefix := range likelyReadOnlyPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func isSensitivePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	for _, hint := range sensitivePathHints {
		if strings.Contains(lower, strings.ToLower(hint)) {
			return true
		}
	}
	return false
}

func applyPolicy(env Environment, cfg Config, plan CommandPlan, guardedShell bool) CommandPlan {
	mode := env.SafetyMode
	if mode == "" {
		mode = cfg.DefaultSafetyMode
	}
	if mode == "" {
		mode = SafetyModeSafeApproval
	}
	requires := plan.RequiresApproval
	if guardedShell {
		requires = true
		plan.ApprovalReason = "自由命令必须经用户确认后执行。"
	}
	switch mode {
	case SafetyModeStrict:
		requires = true
		plan.ApprovalReason = "当前环境处于严格审批模式，所有远端操作都需要确认。"
	case SafetyModeObserve:
		if riskRank(plan.RiskLevel) > riskRank(RiskL1) {
			plan.Blocked = true
			plan.BlockReasons = append(plan.BlockReasons, "观察模式只允许 L0/L1 只读操作。")
		} else if guardedShell {
			requires = true
		}
	case SafetyModeSafeApproval:
		if riskRank(plan.RiskLevel) >= riskRank(RiskL2) {
			requires = true
			plan.ApprovalReason = "安全审批模式下，L2 及以上操作需要确认。"
		}
	case SafetyModeMaintenance:
		if riskRank(plan.RiskLevel) >= riskRank(RiskL3) {
			requires = true
			plan.ApprovalReason = "维护窗口仍要求 L3/L4 操作单独确认。"
		}
	case SafetyModeExpert:
		if riskRank(plan.RiskLevel) >= riskRank(RiskL4) {
			requires = true
			plan.ApprovalReason = "专家模式仍要求 L4 高危操作二次确认。"
		}
	}
	plan.RequiresApproval = requires
	if !freedomAllows(env.FreedomLevel, cfg.DefaultFreedomLevel, plan.RiskLevel, guardedShell) {
		plan.Blocked = true
		plan.BlockReasons = append(plan.BlockReasons, "当前环境自由度等级不足以执行该命令；请提升环境 freedom_level 或改用标准操作。")
	}
	return plan
}

func freedomAllows(envLevel, defaultLevel FreedomLevel, risk RiskLevel, guarded bool) bool {
	level := envLevel
	if level == "" {
		level = defaultLevel
	}
	if level == "" {
		level = FreedomF2
	}
	rank := freedomRank(level)
	if guarded {
		if riskRank(risk) <= riskRank(RiskL1) {
			return rank >= freedomRank(FreedomF3)
		}
		if riskRank(risk) <= riskRank(RiskL3) {
			return rank >= freedomRank(FreedomF4)
		}
		return rank >= freedomRank(FreedomF5)
	}
	if riskRank(risk) <= riskRank(RiskL1) {
		return rank >= freedomRank(FreedomF1)
	}
	if riskRank(risk) <= riskRank(RiskL3) {
		return rank >= freedomRank(FreedomF4)
	}
	return rank >= freedomRank(FreedomF5)
}

func riskRank(r RiskLevel) int {
	switch r {
	case RiskL0:
		return 0
	case RiskL1:
		return 1
	case RiskL2:
		return 2
	case RiskL3:
		return 3
	case RiskL4:
		return 4
	default:
		return 2
	}
}

func freedomRank(f FreedomLevel) int {
	switch f {
	case FreedomF0:
		return 0
	case FreedomF1:
		return 1
	case FreedomF2:
		return 2
	case FreedomF3:
		return 3
	case FreedomF4:
		return 4
	case FreedomF5:
		return 5
	default:
		return 2
	}
}

func maxRisk(a, b RiskLevel) RiskLevel {
	if riskRank(b) > riskRank(a) {
		return b
	}
	return a
}

func newCommandPlan(env Environment, cfg Config, command, purpose, expected string, guarded bool) CommandPlan {
	finding := analyzeCommand(command)
	maxOutput := cfg.DefaultMaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 64 * 1024
	}
	timeout := cfg.DefaultTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	plan := CommandPlan{
		EnvironmentID:  env.ID,
		Command:        strings.TrimSpace(command),
		Purpose:        strings.TrimSpace(purpose),
		ExpectedEffect: strings.TrimSpace(expected),
		RiskLevel:      finding.Risk,
		ReadOnly:       finding.ReadOnly,
		TimeoutSeconds: timeout,
		MaxOutputBytes: maxOutput,
		Warnings:       finding.Warnings,
		Blocked:        finding.Blocked,
		BlockReasons:   finding.BlockReasons,
		AnalyzedAt:     time.Now(),
	}
	if plan.Purpose == "" {
		plan.Purpose = "执行环境诊断命令。"
	}
	if plan.ExpectedEffect == "" {
		plan.ExpectedEffect = "查看环境状态或日志，不应修改目标环境，除非风险分析指出存在写操作。"
	}
	return applyPolicy(env, cfg, plan, guarded)
}
