// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package envmanagertool provides guarded environment-management tools for
// Linux, Docker, Kubernetes, Go, and Python environments.
package envmanagertool

import "time"

// SafetyMode controls which operations may run without explicit HITL approval.
type SafetyMode string

const (
	SafetyModeObserve      SafetyMode = "observe"
	SafetyModeSafeApproval SafetyMode = "safe_approval"
	SafetyModeStrict       SafetyMode = "strict_approval"
	SafetyModeMaintenance  SafetyMode = "maintenance_window"
	SafetyModeExpert       SafetyMode = "expert"
)

// RiskLevel describes operation impact. Higher numbers are more dangerous.
type RiskLevel string

const (
	RiskL0 RiskLevel = "L0"
	RiskL1 RiskLevel = "L1"
	RiskL2 RiskLevel = "L2"
	RiskL3 RiskLevel = "L3"
	RiskL4 RiskLevel = "L4"
)

// FreedomLevel describes how much command freedom an environment allows.
type FreedomLevel string

const (
	FreedomF0 FreedomLevel = "F0"
	FreedomF1 FreedomLevel = "F1"
	FreedomF2 FreedomLevel = "F2"
	FreedomF3 FreedomLevel = "F3"
	FreedomF4 FreedomLevel = "F4"
	FreedomF5 FreedomLevel = "F5"
)

// Environment describes a managed target. Secrets are intentionally references
// or local paths owned by the platform process, never model-visible secret values.
type Environment struct {
	ID             string       `json:"environment_id"`
	Name           string       `json:"name"`
	Type           string       `json:"type"`
	ConnectionType string       `json:"connection_type"`
	Host           string       `json:"host,omitempty"`
	Port           int          `json:"port,omitempty"`
	Username       string       `json:"username,omitempty"`
	KeyPath        string       `json:"key_path,omitempty"`
	PasswordEnv    string       `json:"password_env,omitempty"`
	SafetyMode     SafetyMode   `json:"safety_mode,omitempty"`
	FreedomLevel   FreedomLevel `json:"freedom_level,omitempty"`
	Capabilities   []string     `json:"capabilities,omitempty"`
	Tags           []string     `json:"tags,omitempty"`
	AllowedPaths   []string     `json:"allowed_paths,omitempty"`
	AllowExecute   bool         `json:"allow_execute,omitempty"`
	Notes          string       `json:"notes,omitempty"`
}

// Config configures the toolset from YAML args.
type Config struct {
	ConfigPath            string       `json:"config_path,omitempty"`
	DefaultSafetyMode     SafetyMode   `json:"default_safety_mode,omitempty"`
	DefaultFreedomLevel   FreedomLevel `json:"default_freedom_level,omitempty"`
	DefaultMaxOutputBytes int          `json:"default_max_output_bytes,omitempty"`
	DefaultTimeoutSeconds int          `json:"default_timeout_seconds,omitempty"`
	AllowLocal            bool         `json:"allow_local,omitempty"`
	DryRunDefault         bool         `json:"dry_run_default,omitempty"`
}

// ConfigFile is the JSON file format loaded by the toolset.
type ConfigFile struct {
	Environments []Environment `json:"environments"`
}

// Operation declares one standard platform-controlled operation.
type Operation struct {
	ID                 string            `json:"operation_id"`
	Name               string            `json:"name"`
	Category           string            `json:"category"`
	Capability         string            `json:"capability"`
	RiskLevel          RiskLevel         `json:"risk_level"`
	FreedomLevel       FreedomLevel      `json:"minimum_freedom_level"`
	Description        string            `json:"description"`
	Parameters         map[string]string `json:"parameters,omitempty"`
	RequiresApproval   bool              `json:"requires_approval"`
	DefaultTimeoutSecs int               `json:"default_timeout_seconds"`
}

// CommandPlan is the normalized representation of an operation before execution.
type CommandPlan struct {
	EnvironmentID    string         `json:"environment_id"`
	OperationID      string         `json:"operation_id,omitempty"`
	Command          string         `json:"command"`
	Purpose          string         `json:"purpose"`
	ExpectedEffect   string         `json:"expected_effect"`
	RiskLevel        RiskLevel      `json:"risk_level"`
	ReadOnly         bool           `json:"read_only"`
	RequiresApproval bool           `json:"requires_approval"`
	ApprovalReason   string         `json:"approval_reason,omitempty"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	MaxOutputBytes   int            `json:"max_output_bytes"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	Warnings         []string       `json:"warnings,omitempty"`
	Blocked          bool           `json:"blocked"`
	BlockReasons     []string       `json:"block_reasons,omitempty"`
	AnalyzedAt       time.Time      `json:"analyzed_at"`
}

// ExecutionResult is returned by execution tools.
type ExecutionResult struct {
	Status          string      `json:"status"`
	Plan            CommandPlan `json:"plan"`
	ExitCode        int         `json:"exit_code,omitempty"`
	Output          string      `json:"output,omitempty"`
	OutputTruncated bool        `json:"output_truncated,omitempty"`
	DryRun          bool        `json:"dry_run"`
	StartedAt       time.Time   `json:"started_at,omitempty"`
	FinishedAt      time.Time   `json:"finished_at,omitempty"`
	Audit           AuditRecord `json:"audit"`
}

// AuditRecord is the minimal structured audit entry returned by the tool.
type AuditRecord struct {
	AuditID       string    `json:"audit_id"`
	EnvironmentID string    `json:"environment_id"`
	OperationID   string    `json:"operation_id,omitempty"`
	RiskLevel     RiskLevel `json:"risk_level"`
	Approved      bool      `json:"approved"`
	DryRun        bool      `json:"dry_run"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Status        string    `json:"status"`
	OutputBytes   int       `json:"output_bytes"`
}

func defaultConfig() Config {
	return Config{
		DefaultSafetyMode:     SafetyModeSafeApproval,
		DefaultFreedomLevel:   FreedomF2,
		DefaultMaxOutputBytes: 64 * 1024,
		DefaultTimeoutSeconds: 30,
		DryRunDefault:         true,
	}
}
