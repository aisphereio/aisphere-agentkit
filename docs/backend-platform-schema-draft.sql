-- 后台平台数据模型草案。
-- 注意：这是设计草案，不建议直接作为生产 migration 使用。
-- 正式实现时建议由 GORM model 或 migration 工具生成，并按 PostgreSQL/MySQL/SQLite 差异拆分。

create table tenants (
  id text primary key,
  name text not null,
  status text not null default 'active',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table users (
  id text primary key,
  tenant_id text not null references tenants(id),
  username text not null,
  email text,
  password_hash text,
  display_name text,
  status text not null default 'active',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, username),
  unique (tenant_id, email)
);

create table roles (
  id text primary key,
  tenant_id text not null references tenants(id),
  name text not null,
  description text,
  created_at timestamptz not null default now(),
  unique (tenant_id, name)
);

create table user_roles (
  tenant_id text not null references tenants(id),
  user_id text not null references users(id),
  role_id text not null references roles(id),
  created_at timestamptz not null default now(),
  primary key (user_id, role_id)
);

create table api_keys (
  id text primary key,
  tenant_id text not null references tenants(id),
  user_id text not null references users(id),
  name text not null,
  key_hash text not null,
  scopes jsonb not null default '[]',
  status text not null default 'active',
  expires_at timestamptz,
  last_used_at timestamptz,
  created_at timestamptz not null default now()
);


create table projects (
  id text primary key,
  tenant_id text not null references tenants(id),
  owner_user_id text,
  name text not null,
  display_name text,
  description text,
  app_name text,
  status text not null default 'active',
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index idx_projects_tenant_owner on projects(tenant_id, owner_user_id);
create index idx_projects_tenant_app on projects(tenant_id, app_name);
create index idx_projects_tenant_status on projects(tenant_id, status);

create table project_members (
  tenant_id text not null references tenants(id),
  project_id text not null references projects(id),
  user_id text not null,
  role text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, project_id, user_id)
);

create table runs (
  id text primary key,
  tenant_id text not null references tenants(id),
  app_name text not null,
  user_id text not null,
  session_id text,
  invocation_id text,
  status text not null,
  input_summary text,
  model_ref text,
  metadata_json jsonb not null default '{}',
  error_message text,
  started_at timestamptz not null default now(),
  finished_at timestamptz,
  updated_at timestamptz not null default now()
);

create index idx_runs_tenant_app_user on runs(tenant_id, app_name, user_id);
create index idx_runs_session on runs(tenant_id, session_id);
create index idx_runs_status on runs(tenant_id, status);

create table run_steps (
  id text primary key,
  tenant_id text not null references tenants(id),
  run_id text not null references runs(id),
  step_index int not null,
  kind text not null,
  status text not null,
  name text,
  payload_json jsonb not null default '{}',
  error_message text,
  started_at timestamptz not null default now(),
  finished_at timestamptz
);

create index idx_run_steps_run on run_steps(run_id, step_index);

create table approval_requests (
  id text primary key,
  tenant_id text not null references tenants(id),
  run_id text references runs(id),
  session_id text,
  user_id text not null,
  kind text not null,
  title text,
  payload_json jsonb not null default '{}',
  status text not null default 'pending',
  created_at timestamptz not null default now(),
  expires_at timestamptz,
  decided_at timestamptz,
  decided_by text,
  decision_payload_json jsonb not null default '{}'
);

create index idx_approval_pending on approval_requests(tenant_id, status, created_at);
create index idx_approval_run on approval_requests(tenant_id, run_id);

create table memory_entries (
  id text primary key,
  tenant_id text not null references tenants(id),
  app_name text,
  user_id text not null,
  source_session_id text,
  source_event_id text,
  type text not null,
  content text not null,
  metadata_json jsonb not null default '{}',
  confidence numeric(4,3),
  importance int not null default 0,
  created_by text not null default 'agent',
  status text not null default 'active',
  expires_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index idx_memory_user on memory_entries(tenant_id, app_name, user_id, status);
create index idx_memory_type on memory_entries(tenant_id, type, status);

create table memory_chunks (
  id text primary key,
  tenant_id text not null references tenants(id),
  memory_id text not null references memory_entries(id),
  chunk_text text not null,
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now()
);

create table skills (
  id text primary key,
  tenant_id text not null references tenants(id),
  name text not null,
  display_name text,
  description text,
  category text,
  visibility text not null default 'tenant',
  status text not null default 'draft',
  current_version_id text,
  created_by text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, name)
);

create table skill_versions (
  id text primary key,
  tenant_id text not null references tenants(id),
  skill_id text not null references skills(id),
  version text not null,
  changelog text,
  content_object_key text not null,
  frontmatter_json jsonb not null default '{}',
  instructions_text text,
  checksum text,
  status text not null default 'draft',
  created_by text,
  created_at timestamptz not null default now(),
  unique (skill_id, version)
);

create table skill_resources (
  id text primary key,
  tenant_id text not null references tenants(id),
  skill_version_id text not null references skill_versions(id),
  path text not null,
  object_key text not null,
  mime_type text,
  size_bytes bigint,
  checksum text,
  created_at timestamptz not null default now(),
  unique (skill_version_id, path)
);

create table model_providers (
  id text primary key,
  tenant_id text not null references tenants(id),
  name text not null,
  provider_type text not null,
  base_url text,
  status text not null default 'active',
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, name)
);

create table model_credentials (
  id text primary key,
  tenant_id text not null references tenants(id),
  provider_id text not null references model_providers(id),
  name text not null,
  secret_ref text not null,
  status text not null default 'active',
  created_at timestamptz not null default now()
);

create table model_specs (
  id text primary key,
  tenant_id text not null references tenants(id),
  provider_id text not null references model_providers(id),
  credential_id text references model_credentials(id),
  name text not null,
  model text not null,
  context_window bigint,
  max_output_tokens int,
  supports_tools bool not null default false,
  supports_json_schema bool not null default false,
  supports_streaming bool not null default true,
  generation_json jsonb not null default '{}',
  extra_body_json jsonb not null default '{}',
  status text not null default 'active',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, name)
);

create table model_aliases (
  id text primary key,
  tenant_id text not null references tenants(id),
  alias text not null,
  model_spec_id text not null references model_specs(id),
  scope text not null default 'tenant',
  app_name text,
  user_id text,
  created_at timestamptz not null default now(),
  unique (tenant_id, alias, scope, app_name, user_id)
);

create table secrets (
  id text primary key,
  tenant_id text not null references tenants(id),
  name text not null,
  type text not null,
  encrypted_value bytea,
  metadata_json jsonb not null default '{}',
  status text not null default 'active',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, name)
);

create table environments (
  id text primary key,
  tenant_id text not null references tenants(id),
  name text not null,
  type text not null,
  connection_type text not null,
  host text,
  port int,
  username text,
  secret_ref text,
  safety_mode text not null default 'safe_approval',
  freedom_level text not null default 'F2',
  allow_execute bool not null default false,
  status text not null default 'active',
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, name)
);

create table environment_capabilities (
  tenant_id text not null references tenants(id),
  environment_id text not null references environments(id),
  capability text not null,
  created_at timestamptz not null default now(),
  primary key (environment_id, capability)
);

create table operation_catalog (
  id text primary key,
  tenant_id text,
  operation_id text not null,
  category text not null,
  capability text not null,
  risk_level text not null,
  minimum_freedom_level text not null,
  params_schema_json jsonb not null default '{}',
  command_template text,
  requires_approval bool not null default false,
  enabled bool not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, operation_id)
);

create table environment_audit_logs (
  id text primary key,
  tenant_id text not null references tenants(id),
  environment_id text not null references environments(id),
  user_id text,
  session_id text,
  run_id text references runs(id),
  operation_id text,
  command_preview text,
  risk_level text,
  approved bool not null default false,
  dry_run bool not null default true,
  status text not null,
  output_object_key text,
  output_bytes bigint,
  started_at timestamptz not null default now(),
  finished_at timestamptz,
  metadata_json jsonb not null default '{}'
);

create index idx_env_audit_env on environment_audit_logs(tenant_id, environment_id, started_at desc);
create index idx_env_audit_run on environment_audit_logs(tenant_id, run_id);

create table artifacts (
  id text primary key,
  tenant_id text not null references tenants(id),
  app_name text,
  user_id text,
  session_id text,
  file_name text not null,
  current_version int not null default 1,
  status text not null default 'active',
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table artifact_versions (
  id text primary key,
  tenant_id text not null references tenants(id),
  artifact_id text not null references artifacts(id),
  version int not null,
  object_key text not null,
  mime_type text,
  size_bytes bigint,
  checksum text,
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now(),
  unique (artifact_id, version)
);

-- P1.1 Run / Approval Store MVP
-- SQLite/GORM auto migration is the current implementation source of truth.
-- This SQL is a readable draft for later PostgreSQL migration scripts.

CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  app_name TEXT,
  user_id TEXT,
  session_id TEXT,
  status TEXT NOT NULL,
  input_summary TEXT,
  model_ref TEXT,
  error_message TEXT,
  metadata_json TEXT,
  started_at TIMESTAMP NOT NULL,
  finished_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_tenant_id ON runs (tenant_id);
CREATE INDEX IF NOT EXISTS idx_runs_user_id ON runs (user_id);
CREATE INDEX IF NOT EXISTS idx_runs_session_id ON runs (session_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs (status);

CREATE TABLE IF NOT EXISTS run_steps (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  payload_json TEXT,
  error_message TEXT,
  started_at TIMESTAMP NOT NULL,
  finished_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_steps_tenant_id ON run_steps (tenant_id);
CREATE INDEX IF NOT EXISTS idx_run_steps_run_id ON run_steps (run_id);
CREATE INDEX IF NOT EXISTS idx_run_steps_kind ON run_steps (kind);
CREATE INDEX IF NOT EXISTS idx_run_steps_status ON run_steps (status);

CREATE TABLE IF NOT EXISTS approval_requests (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  run_id TEXT,
  session_id TEXT,
  user_id TEXT,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  payload_json TEXT,
  reason TEXT,
  decided_by TEXT,
  decided_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_approval_requests_tenant_id ON approval_requests (tenant_id);
CREATE INDEX IF NOT EXISTS idx_approval_requests_run_id ON approval_requests (run_id);
CREATE INDEX IF NOT EXISTS idx_approval_requests_session_id ON approval_requests (session_id);
CREATE INDEX IF NOT EXISTS idx_approval_requests_user_id ON approval_requests (user_id);
CREATE INDEX IF NOT EXISTS idx_approval_requests_kind ON approval_requests (kind);
CREATE INDEX IF NOT EXISTS idx_approval_requests_status ON approval_requests (status);
