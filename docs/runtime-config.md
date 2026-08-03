# Runtime Config Layer

This project now has a Viper-backed runtime configuration layer in `internal/runtimeconfig`.

## Load order

`cmd/internal/adkcli` loads configuration before scanning `root_agent.yaml`:

1. `ADK_CONFIG=/path/to/adk.yaml`, if set.
2. `adk.yaml` in the current working directory.
3. `adk.yaml` in `./config`.
4. `adk.yaml` in `./.adk`.
5. Built-in local defaults.

Environment variables can override keys with the `ADK_` prefix. For example:

```bash
export ADK_STORAGE_SESSION_TYPE=filesystem
export ADK_STORAGE_ROOT=./data
export ADK_MODELS_DEFAULT=deepseek
```

Provider keys are still normally resolved by the model spec's `api_key_env`, for example `OPENAI_API_KEY` or `DEEPSEEK_API_KEY`.

## Root agent model reference

A `root_agent.yaml` no longer needs to contain base URLs or API keys. It can reference a model spec or alias:

```yaml
agent_class: LlmAgent
name: novel_opening
model: default
instruction: |
  You are a novel opening assistant.
```

Or:

```yaml
model: deepseek
```

The global `adk.yaml` resolves that model reference:

```yaml
models:
  default: default
  aliases:
    default: openai.gpt4_1_mini
    deepseek: deepseek.chat
  specs:
    openai.gpt4_1_mini:
      provider: openai
      model: gpt-4.1-mini
      base_url: https://api.openai.com/v1
      api_key_env: OPENAI_API_KEY
      context_window: 128000
      generation:
        temperature: 0.2
        max_output_tokens: 8192
```

## Storage implementations

Current implemented backends:

```yaml
storage:
  session:
    type: filesystem # or inmemory, sqlite, database
  artifact:
    type: filesystem # or inmemory
  memory:
    type: filesystem # or inmemory
```

Filesystem is intended for local development and single-process testing. `storage.session.type=sqlite` exposes the existing GORM database session service through runtime config without requiring an external database. `storage.session.type=database` can now inherit `storage.database.type=postgres` for production PostgreSQL-backed sessions. `mysql` remains a reserved config value until its driver patch.

### SQLite session backend

Use this when you want durable sessions/events/state without running PostgreSQL yet:

```yaml
storage:
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true

  session:
    type: database
```

Or configure the session backend directly:

```yaml
storage:
  session:
    type: sqlite
    dsn: ./.adk/data/database/adk-session.db
    auto_migrate: true
```

Rules:

- `storage.session.type=database` inherits `storage.database.type/dsn/dsn_env/auto_migrate`.
- `storage.session.type=sqlite` uses `storage.session.dsn` first, then `storage.database.dsn`, then a local `adk.db`.
- `auto_migrate: true` calls `session/database.AutoMigrate` for session tables.
- SQLite supports `:memory:` and `file::memory:` DSNs for tests.


### PostgreSQL session backend

Production configuration should use PostgreSQL as the shared facts database:

```yaml
storage:
  database:
    type: postgres
    dsn_env: ADK_DATABASE_DSN
    auto_migrate: true
    max_open_conns: 30
    max_idle_conns: 10
    conn_max_lifetime: 1h
    conn_max_idle_time: 15m

  session:
    type: database
```

`gorm.io/driver/postgres` uses pgx underneath. This same `storage.database` is also used by platform users, projects, runs, and approvals.

## Launcher defaults

If no CLI args are provided, `cmd/internal/adkcli` builds launcher args from config:

```yaml
server:
  web:
    enabled: true
    port: 8080
  api:
    enabled: true
    path_prefix: /api
```

This is equivalent to:

```bash
go run ./cmd/internal/adkcli web -port 8080 api -path_prefix /api
```

Explicit CLI args still win.

## Context window

`runtime.context_window` and `models.specs.*.context_window` are now declared in config and resolved with the model spec. The current patch records the value as a model capability/planning field; the next step is adding a `ContextBudgetProcessor` that compresses or truncates history before the LLM request is built.

## Platform backend target configuration

The current implementation supports `filesystem` and `inmemory` storage for session, artifact, and memory; `minio`/`s3` storage for artifacts; and `sqlite`/`postgres` database-backed sessions. Platform users, projects, runs, and approvals now use the shared `storage.database` connection.

Target production shape:

```yaml
storage:
  database:
    type: postgres
    dsn_env: ADK_DATABASE_DSN
    auto_migrate: true

  session:
    type: database

  artifact:
    type: minio
    endpoint: 127.0.0.1:9000
    bucket: adk-artifacts
    access_key_env: MINIO_ACCESS_KEY
    secret_key_env: MINIO_SECRET_KEY
    use_ssl: false
    prefix: artifacts
    create_bucket: true
    path_style: true

  memory:
    type: postgres

  kv:
    type: redis

  object:
    type: minio
    endpoint: 127.0.0.1:9000
    bucket: adk
    access_key_env: MINIO_ACCESS_KEY
    secret_key_env: MINIO_SECRET_KEY
    use_ssl: false
```

The intended responsibility split is:

- PostgreSQL stores durable facts: users, sessions, events, runs, approvals, model metadata, skill metadata, environments, memory entries, artifact metadata, and audit logs.
- Redis stores runtime state: SSE stream buffers, resumable run continuation data, locks, short-lived tokens, rate limits, and cache.
- MinIO/S3 stores large objects: artifacts, uploaded files, skill package resources, large traces, environment operation output, and diagnosis reports.

Do not store API keys, SSH keys, kubeconfig contents, or database credentials in committed YAML files. Use environment variables or a Secret Store reference.

See also:

- `docs/backend-platform-design.md`
- `docs/backend-platform-config-examples.md`
- `docs/backend-platform-implementation-plan.md`


## PostgreSQL 自动建库

当平台主库使用 PostgreSQL 时，可以开启：

```yaml
storage:
  database:
    type: postgres
    dsn_env: ADK_DATABASE_DSN
    auto_create_database: true
    maintenance_database: postgres
    auto_migrate: true
```

启动时会先连接维护库 `postgres`，检测目标库是否存在；不存在则自动 `CREATE DATABASE`，然后再连接目标库执行 GORM AutoMigrate。执行账号需要具备 `CREATEDB` 权限。
