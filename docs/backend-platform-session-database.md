# Session Database Backend（P0.3）

> 本文档记录 Session database backend 接入方式。P0.3 先用 SQLite 打通链路；P1.2 已接入 PostgreSQL driver，生产建议使用 PostgreSQL。

## 1. 已实现能力

本轮补丁新增：

```text
internal/runtimeconfig/config.go
  - StorageConfig.Database
  - DatabaseConfig
  - ServiceConfig.DSNEnv
  - ServiceConfig.AutoMigrate
  - storage.session.type=sqlite
  - storage.session.type=database
```

并新增测试：

```text
internal/runtimeconfig/config_test.go
```

已验证的行为：

- 默认 `filesystem` session 不变；
- `storage.session.type=sqlite` 可以创建 GORM SQLite session service；
- `storage.session.type=database` 可以继承 `storage.database.*`；
- `auto_migrate: true` 会调用 `session/database.AutoMigrate`；
- `postgres/postgresql/pg` 已支持；
- `mysql` 暂时 fail fast，等待后续 driver patch。

## 2. 推荐本地配置

### 2.1 保持默认 filesystem

```yaml
storage:
  root: ./.adk/data
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true
  session:
    type: filesystem
    root: ./.adk/data/sessions
```

这是当前 `adk.yaml` 默认形态。即使配置了 `storage.database`，只要 `storage.session.type=filesystem`，Session 仍走文件系统。

### 2.2 启用 SQLite Session

```yaml
storage:
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true

  session:
    type: database
```

或者直接：

```yaml
storage:
  session:
    type: sqlite
    dsn: ./.adk/data/database/adk-session.db
    auto_migrate: true
```

## 3. DSN 解析优先级

`storage.session.type=sqlite` 或 `database` 时，DSN 解析顺序：

```text
storage.session.dsn
storage.session.dsn_env 对应环境变量
storage.database.dsn
storage.database.dsn_env 对应环境变量
默认 ./.adk/data/database/adk.db
```

`storage.session.type=database` 时，数据库类型解析顺序：

```text
storage.session.type 如果是 sqlite/postgres/mysql，则直接使用
storage.database.type 如果 session.type 是 database/db/sql/relational，则继承这里
默认 sqlite
```

## 4. AutoMigrate

```yaml
storage:
  database:
    auto_migrate: true
```

或：

```yaml
storage:
  session:
    auto_migrate: true
```

任一位置为 `true`，就会对 session database 调用：

```go
sessiondatabase.AutoMigrate(service)
```

当前只迁移 session database 已有表：

```text
storage_sessions
storage_events
storage_app_states
storage_user_states
```

后续 `users/runs/approvals` 会有自己的 platform migration。

## 5. PostgreSQL Session 配置

P1.2 已支持 PostgreSQL。目标配置：

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

`session.type=database` 会继承 `storage.database.type/dsn/dsn_env`，因此 session、events、app_state、user_state 会落到同一个 PostgreSQL。

MySQL 仍是后续兼容项，当前会 fail fast，不会静默降级到 filesystem。
