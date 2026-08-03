# 后台平台配置示例

> 本文档给出不同阶段的 `adk.yaml` 配置样例。当前代码可能尚未全部支持这些字段；它们作为平台化开发的目标配置格式。

## 1. 本地开发：filesystem

```yaml
root: ./.adk

server:
  web:
    enabled: true
    port: 8080
  api:
    enabled: true
    path_prefix: /api
    frontend_address: http://localhost:4200,http://localhost:8080
    sse_write_timeout: 10m
    trace_capacity: 10000
    resumable_runs:
      enabled: true
      mode: standalone
      key_prefix: adk:run
      ttl: 6h
      block_timeout: 15s

auth:
  mode: none

storage:
  root: ./.adk/data
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true
  session:
    type: filesystem
    root: ./.adk/data/sessions
  artifact:
    type: filesystem
    root: ./.adk/data/artifacts
  memory:
    type: filesystem
    root: ./.adk/data/memory
  kv:
    type: filesystem
    root: ./.adk/data/kv
  object:
    type: filesystem
    root: ./.adk/data/objects
```

## 2. 本地开发：SQLite Session

当你希望会话和事件持久化到单个数据库文件，但还不想部署 PostgreSQL，可以使用 SQLite session backend：

```yaml
storage:
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true

  session:
    type: database

  artifact:
    type: filesystem
    root: ./.adk/data/artifacts

  memory:
    type: filesystem
    root: ./.adk/data/memory
```

等价直接写法：

```yaml
storage:
  session:
    type: sqlite
    dsn: ./.adk/data/database/adk-session.db
    auto_migrate: true
```

SQLite 路径已在 P0.3 中接入 runtimeconfig；PostgreSQL 已在 P1.2 中接入。MySQL 仍是后续兼容项。

## 3. 单机生产目标：PostgreSQL + Redis + MinIO

```yaml
root: ./.adk

server:
  web:
    enabled: true
    port: 8080
  api:
    enabled: true
    path_prefix: /api
    frontend_address: https://your-web.example.com
    sse_write_timeout: 10m
    trace_capacity: 10000
    resumable_runs:
      enabled: true
      mode: redis
      addrs:
        - 127.0.0.1:6379
      username: ""
      password_env: ADK_REDIS_PASSWORD
      db: 0
      key_prefix: adk:run
      ttl: 6h
      block_timeout: 15s

auth:
  mode: dev_token
  dev_tokens:
    - token_env: ADK_DEV_TOKEN
      tenant_id: default
      user_id: admin
      roles: [owner]
      scopes: ["*"]

storage:
  database:
    type: postgres
    dsn_env: ADK_DATABASE_DSN
    auto_migrate: true
    auto_create_database: true
    maintenance_database: postgres
    max_open_conns: 30
    max_idle_conns: 10
    conn_max_lifetime: 1h
    conn_max_idle_time: 15m

  session:
    type: database

  artifact:
    type: minio

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

环境变量：

```bash
export ADK_DATABASE_DSN='postgres://adk:adk@127.0.0.1:5432/adk?sslmode=disable'
export ADK_REDIS_PASSWORD='change-me'
export ADK_DEV_TOKEN='dev-local-token'
export MINIO_ACCESS_KEY='minioadmin'
export MINIO_SECRET_KEY='change-me'
```

注意：不要把真实密码写进仓库里的 `adk.yaml`。

## 4. Model Registry 目标配置

当前 YAML 模型配置仍保留作为 fallback：

```yaml
models:
  source: database_with_yaml_fallback
  default: default
  aliases:
    default: deepseek_chat
  specs:
    deepseek_chat:
      provider: openai
      model: deepseek_v4
      base_url: http://127.0.0.1:30080/v1
      api_key_env: DEEPSEEK_API_KEY
      context_window: 100000
      generation:
        temperature: 0.7
        max_output_tokens: 32768
```

服务化后，DB 中的 model alias 优先级建议高于 YAML，但必须保留 YAML fallback，保证本地开发简单。

## 5. Environment Store 目标配置

```yaml
environments:
  source: service # file | service
  file:
    path: ./agents/env_manager/env/environments.example.json
  service:
    default_safety_mode: safe_approval
    default_freedom_level: F2
    dry_run_default: true
    default_timeout_seconds: 30
    default_max_output_bytes: 65536
```

EnvToolset 第一阶段仍支持 file source，后续 service source 从 `/environments` 后台读取。

## 6. Skill Registry 目标配置

```yaml
skills:
  enabled: true
  root: ./skills
  preload: complete
  source: registry_with_filesystem_builtin
  object_prefix: skills/
```

含义：

- `./skills` 是内置 Skill；
- DB+MinIO 是用户 Skill；
- Agent 使用统一 registry resolve skill ID。
