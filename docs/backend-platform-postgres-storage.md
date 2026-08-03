# PostgreSQL Storage Backend（P1.2）

本轮目标：把后台平台的事实数据从 SQLite-first 推进到 PostgreSQL-first。当前实现选择：

```text
GORM + gorm.io/driver/postgres + pgx
```

其中 `gorm.io/driver/postgres` 底层使用 pgx，适合继续复用现有 `session/database`、`runs`、`approvals` 这些 GORM 模型。后续如果某些高频查询需要更强的 SQL 可控性，可以在局部模块引入：

```text
pgxpool + sqlc + goose
```

## 1. 已落地能力

新增依赖：

```text
gorm.io/driver/postgres
```

已支持 PostgreSQL 的位置：

```text
storage.database.type=postgres
  - platform users / tenants / roles
  - platform projects / project_members
  - platform runs / run_steps
  - platform approval_requests

storage.session.type=database + storage.database.type=postgres
  - sessions
  - events
  - app_states
  - user_states
```

SQLite 仍然保留，适合本地临时开发和单元测试。

## 2. 推荐配置

```yaml
storage:
  root: ./.adk/data
  database:
    type: postgres
    dsn_env: ADK_DATABASE_DSN
    auto_migrate: true
    # 开发环境建议打开：目标库不存在时，先连接 maintenance_database 自动 CREATE DATABASE。
    auto_create_database: true
    maintenance_database: postgres
    max_open_conns: 30
    max_idle_conns: 10
    conn_max_lifetime: 1h
    conn_max_idle_time: 15m

  session:
    type: database
```

环境变量：

```powershell
$env:ADK_DATABASE_DSN="host=CHANGE_ME_HOST port=30432 user=postgres password=Postgres@123456 dbname=adk sslmode=disable"
```

Linux/macOS：

```bash
export ADK_DATABASE_DSN='postgres://adk:adk@127.0.0.1:5432/adk?sslmode=disable'
```

## 3. 启动时自动建库与自动迁移

当：

```yaml
storage:
  database:
    type: postgres
    auto_create_database: true
    maintenance_database: postgres
```

启动时会先解析 `ADK_DATABASE_DSN` 里的目标数据库，例如：

```text
host=CHANGE_ME_HOST port=30432 user=postgres password=Postgres@123456 dbname=adk sslmode=disable
```

目标库是 `adk`。如果连接目标库失败且数据库不存在，平台会先连接维护库 `postgres`，执行：

```sql
CREATE DATABASE "adk";
```

然后再连接 `adk` 做表结构迁移。

注意：执行用户必须有 `CREATEDB` 权限。默认超级用户 `postgres` 一般可以；普通业务用户通常需要 DBA 先授权或手动建库。

## 4. 启动时自动迁移

当：

```yaml
storage:
  database:
    auto_migrate: true
```

REST API 启动时会自动迁移平台表：

```text
tenants
users
roles
user_roles
projects
project_members
runs
run_steps
approval_requests
```

Session backend 使用 PostgreSQL 时，`runtimeconfig.BuildServices()` 会迁移 ADK session 表：

```text
sessions
events
app_states
user_states
```

## 5. 默认用户写入 PG

当前认证还是 `auth.mode=none` 或 `dev_token`，但服务启动后会把当前可用 Principal 引导写入 PG。

`auth.mode=none` 会写入：

```text
tenant: default
user: admin
role: owner
```

`auth.mode=dev_token` 会根据 `auth.dev_tokens` 写入：

```text
tenant_id
user_id
roles
```

这一步是为了让后续 Session、Project、Skill、Environment、Audit 都能绑定真实 owner，而不是继续散落在前端参数里。

## 6. 新增平台 API

用户/租户：

```http
GET  /api/platform/tenant
POST /api/platform/tenants
GET  /api/platform/users
POST /api/platform/users
GET  /api/platform/users/{user_id}
PATCH /api/platform/users/{user_id}
```

项目：

```http
GET  /api/platform/projects
POST /api/platform/projects
GET  /api/platform/projects/{project_id}
PATCH /api/platform/projects/{project_id}
POST /api/platform/projects/{project_id}/archive
```

运行/审批沿用上一轮接口：

```http
GET/POST/PATCH /api/platform/runs...
GET/POST /api/platform/approvals...
```

## 7. 当前边界

已入 PG：

```text
user / tenant / role
project / project_member
session / event / app_state / user_state
run / run_step
approval_request
```

仍未入 PG：

```text
artifact 文件内容：仍建议进入 MinIO，PG 只放元数据
skill 内容：下一步做 DB + MinIO registry
model secret：下一步做 secret_ref，不直接入明文
memory：下一步做 memory_entries + pgvector
环境资产：下一步做 environments / secrets / audit_logs
```

## 8. 验证命令

设置 DSN：

```powershell
$env:ADK_DATABASE_DSN="host=CHANGE_ME_HOST port=30432 user=postgres password=Postgres@123456 dbname=adk sslmode=disable"
```

启动：

```powershell
go run .\cmd\internal\adkcli
```

验证当前身份已经引导入库：

```powershell
curl.exe http://localhost:8080/api/me
curl.exe http://localhost:8080/api/platform/tenant
curl.exe http://localhost:8080/api/platform/users
```

创建项目：

```powershell
curl.exe -X POST http://localhost:8080/api/platform/projects `
  -H "Content-Type: application/json" `
  -d '{"name":"demo-project","display_name":"Demo Project","app_name":"test1"}'
```

验证 PostgreSQL 表：

```sql
select id, name, status from tenants;
select tenant_id, id, username, status from users;
select id, tenant_id, name, app_name, status from projects;
select id, tenant_id, app_name, user_id, session_id, status from runs order by created_at desc limit 5;
select app_name, user_id, id, update_time from sessions order by update_time desc limit 5;
```


## 9. 常见错误

### database "adk" does not exist

开启 `auto_create_database: true` 后会自动尝试创建。若仍失败，通常是当前用户没有 `CREATEDB` 权限。

### permission denied to create database

说明账号能连维护库，但没有建库权限。处理方式：

```sql
ALTER USER postgres CREATEDB;
-- 或者让管理员手动创建：
CREATE DATABASE adk;
```

### dsn_env 为空却回退到 SQLite 路径

`type=postgres` 时不应使用 SQLite 默认路径。新版本会在 DSN 为空时直接报清晰错误。推荐直接使用：

```yaml
storage:
  database:
    type: postgres
    dsn: "host=CHANGE_ME_HOST port=30432 user=postgres password=Postgres@123456 dbname=adk sslmode=disable"
```

先跑通后再切回 `dsn_env`。
