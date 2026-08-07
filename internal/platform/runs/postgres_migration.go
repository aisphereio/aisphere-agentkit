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

package runs

import (
	"fmt"

	"gorm.io/gorm"
)

const runtimeFactsMigrationLock int64 = 720260806001

type runtimeFactsMigration struct {
	version    int64
	statements []string
}

func migratePostgresRuntimeFacts(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
CREATE TABLE IF NOT EXISTS runtime_schema_migrations (
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`).Error; err != nil {
			return fmt.Errorf("create runtime schema migrations table: %w", err)
		}
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, runtimeFactsMigrationLock).Error; err != nil {
			return fmt.Errorf("lock runtime schema migration: %w", err)
		}

		for _, migration := range runtimeFactsPostgresMigrations {
			var applied int64
			if err := tx.Raw(
				`SELECT COUNT(*) FROM runtime_schema_migrations WHERE version = ?`,
				migration.version,
			).Scan(&applied).Error; err != nil {
				return fmt.Errorf("read runtime schema migration version %d: %w", migration.version, err)
			}
			if applied > 0 {
				continue
			}
			for _, statement := range migration.statements {
				if err := tx.Exec(statement).Error; err != nil {
					return fmt.Errorf("apply runtime facts schema v%d: %w", migration.version, err)
				}
			}
			if err := tx.Exec(
				`INSERT INTO runtime_schema_migrations(version) VALUES (?)`,
				migration.version,
			).Error; err != nil {
				return fmt.Errorf("record runtime schema migration version %d: %w", migration.version, err)
			}
		}
		return nil
	})
}

var runtimeFactsPostgresMigrations = []runtimeFactsMigration{
	{version: 1, statements: runtimeFactsPostgresV1Statements},
	{version: 2, statements: runtimeFactsPostgresV2Statements},
}

var runtimeFactsPostgresV1Statements = []string{
	`CREATE TABLE runtime_runs (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    project_id VARCHAR(128) NOT NULL DEFAULT '',
    conversation_id VARCHAR(128) NOT NULL DEFAULT '',
    app_name VARCHAR(256) NOT NULL DEFAULT '',
    agent_id VARCHAR(128) NOT NULL DEFAULT '',
    agent_revision VARCHAR(128) NOT NULL DEFAULT '',
    user_id VARCHAR(256) NOT NULL DEFAULT '',
    principal_id VARCHAR(256) NOT NULL DEFAULT '',
    session_id VARCHAR(256) NOT NULL DEFAULT '',
    snapshot_id VARCHAR(64) NOT NULL DEFAULT '',
    current_attempt_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(64) NOT NULL,
    trigger_type VARCHAR(64) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(256),
    input_summary TEXT NOT NULL DEFAULT '',
    model_ref VARCHAR(256) NOT NULL DEFAULT '',
    trace_id VARCHAR(128) NOT NULL DEFAULT '',
    failure_code VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '',
    queued_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_runtime_runs_tenant_idempotency UNIQUE (tenant_id, idempotency_key)
)`,
	`CREATE INDEX idx_runtime_runs_tenant_created ON runtime_runs(tenant_id, created_at DESC)`,
	`CREATE INDEX idx_runtime_runs_status ON runtime_runs(tenant_id, status, created_at DESC)`,
	`CREATE INDEX idx_runtime_runs_project ON runtime_runs(tenant_id, project_id, created_at DESC)`,
	`CREATE INDEX idx_runtime_runs_agent ON runtime_runs(tenant_id, agent_id, created_at DESC)`,
	`CREATE INDEX idx_runtime_runs_session ON runtime_runs(tenant_id, session_id, created_at DESC)`,
	`CREATE INDEX idx_runtime_runs_trace ON runtime_runs(trace_id)`,

	`CREATE TABLE runtime_execution_snapshots (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(64) NOT NULL UNIQUE REFERENCES runtime_runs(id) ON DELETE CASCADE,
    schema_version VARCHAR(64) NOT NULL,
    source_spec_digest VARCHAR(128) NOT NULL,
    snapshot_digest VARCHAR(128) NOT NULL,
    agent_id VARCHAR(128) NOT NULL DEFAULT '',
    agent_revision VARCHAR(128) NOT NULL DEFAULT '',
    model_revision VARCHAR(128) NOT NULL DEFAULT '',
    resolver_version VARCHAR(64) NOT NULL DEFAULT '',
    runtime_engine VARCHAR(64) NOT NULL,
    snapshot_json TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`,
	`CREATE INDEX idx_runtime_snapshots_tenant ON runtime_execution_snapshots(tenant_id, created_at DESC)`,
	`CREATE INDEX idx_runtime_snapshots_digest ON runtime_execution_snapshots(snapshot_digest)`,
	`CREATE INDEX idx_runtime_snapshots_agent ON runtime_execution_snapshots(tenant_id, agent_id, agent_revision)`,

	`CREATE TABLE runtime_run_attempts (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(64) NOT NULL REFERENCES runtime_runs(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    status VARCHAR(64) NOT NULL,
    runtime_engine VARCHAR(64) NOT NULL,
    runtime_build VARCHAR(128) NOT NULL DEFAULT '',
    compiler_version VARCHAR(64) NOT NULL DEFAULT '',
    compiled_plan_digest VARCHAR(128) NOT NULL DEFAULT '',
    sandbox_lease_id VARCHAR(128) NOT NULL DEFAULT '',
    worker_instance VARCHAR(256) NOT NULL DEFAULT '',
    failure_code VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_runtime_attempt_number UNIQUE (run_id, attempt_number)
)`,
	`CREATE INDEX idx_runtime_attempts_tenant_run ON runtime_run_attempts(tenant_id, run_id, attempt_number)`,
	`CREATE INDEX idx_runtime_attempts_status ON runtime_run_attempts(tenant_id, status, created_at DESC)`,
	`CREATE INDEX idx_runtime_attempts_sandbox ON runtime_run_attempts(sandbox_lease_id)`,

	`CREATE TABLE runtime_events (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(64) NOT NULL REFERENCES runtime_runs(id) ON DELETE CASCADE,
    attempt_id VARCHAR(64) NOT NULL DEFAULT '',
    sequence BIGINT NOT NULL,
    event_type VARCHAR(128) NOT NULL,
    event_version VARCHAR(64) NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '',
    trace_id VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_runtime_event_sequence UNIQUE (run_id, sequence)
)`,
	`CREATE INDEX idx_runtime_events_tenant_run ON runtime_events(tenant_id, run_id, sequence)`,
	`CREATE INDEX idx_runtime_events_attempt ON runtime_events(attempt_id, sequence)`,
	`CREATE INDEX idx_runtime_events_type ON runtime_events(tenant_id, event_type, created_at DESC)`,
	`CREATE INDEX idx_runtime_events_trace ON runtime_events(trace_id)`,
}

// V2 is deliberately destructive. AISphere has not shipped this execution
// schema yet, so stale compatibility facts are removed instead of carried
// forward as a second source of truth.
var runtimeFactsPostgresV2Statements = []string{
	`DROP TABLE IF EXISTS run_steps CASCADE`,
	`DROP TABLE IF EXISTS platform_run_steps CASCADE`,
	`DROP TABLE IF EXISTS platform_run_events CASCADE`,
	`DROP TABLE IF EXISTS platform_runs CASCADE`,
}
