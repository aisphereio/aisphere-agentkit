# 后台平台数据模型草案

> 本文档给出第一版平台后台表结构草案。字段命名偏 PostgreSQL，但实现层建议用 GORM model + migration 封装，后续兼容 MySQL/SQLite 时再处理差异。

## 1. 命名约定

- 所有平台表建议加 `tenant_id`，即便第一版只有 `default` tenant。
- 所有 ID 推荐使用 UUID string。
- 大 JSON 字段统一使用 `*_json`。
- 大文本输出不要直接塞 DB，放 MinIO/S3，然后 DB 存 `object_key`。
- 删除优先用 `status=deleted/archived`，少做物理删除。

## 2. 用户、租户、角色

```sql
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
```

第一版内置角色：

| Role | 含义 |
| --- | --- |
| owner | 全部权限 |
| admin | 管理模型、环境、Skill、用户 |
| developer | 创建 Agent、运行 Session、管理项目内 Skill |
| operator | 使用环境管理能力、查看审计 |
| viewer | 只读 |


## 2.5 Project / Workbench

P1.2 已新增 GORM model 和 API。Project 是业务产品层的持久化分组，位于 Session/Run 之上；它可以绑定某个 app/agent，但不是 ADK runtime 对象本身。

```sql
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

create table project_members (
  tenant_id text not null references tenants(id),
  project_id text not null references projects(id),
  user_id text not null,
  role text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, project_id, user_id)
);
```

用途：

```text
项目列表
Workbench 状态入口
项目级 artifact/memory/skill 归属
后续权限与协作
```

## 3. Session 与 Run

`session/database` 已经有自己的 storage model，第一版可以复用，不必重复定义 ADK session 表。

平台额外增加 run 表：

```sql
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
```

建议状态枚举：

```text
queued / running / waiting_approval / completed / failed / cancelled / expired
```

## 4. Approval

```sql
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
```

`kind` 建议：

```text
user_choice
tool_confirmation
environment_operation
free_command
artifact_review
```

## 5. Memory

```sql
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
```

后续接 pgvector 时增加：

```sql
-- create extension if not exists vector;
-- alter table memory_chunks add column embedding vector(1536);
-- create index idx_memory_embedding on memory_chunks using ivfflat (embedding vector_cosine_ops);
```

## 6. Skill Registry

```sql
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
```

内置 Skill 可以使用特殊 tenant：

```text
tenant_id = builtin
visibility = public
status = active
```

## 7. Model Registry

```sql
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
```

## 8. Environment / Secret / Audit

```sql
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
```

## 9. Artifact Metadata

```sql
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
```

对象存储 key 约定：

```text
artifacts/{tenant_id}/{app_name}/{user_id}/{session_id}/{file_name}/{version}
uploads/{tenant_id}/{user_id}/{upload_id}/{filename}
skills/{tenant_id}/{skill_id}/{version}/SKILL.md
skills/{tenant_id}/{skill_id}/{version}/resources/{path}
traces/{tenant_id}/{run_id}/...
env-output/{tenant_id}/{audit_id}.txt
```
