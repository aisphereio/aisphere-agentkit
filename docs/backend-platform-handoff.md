# 后台平台开发交接说明

> 给后续 Codex/开发者的接手说明。请先读 `backend-platform-design.md`，再按本文档推进具体 patch。

## 1. 当前不要直接做的事情

- 不要把所有后台能力一次性重构完。
- 不要删除 filesystem/inmemory 模式。
- 不要把用户、租户、权限字段硬塞进 ADK core 的所有结构体。
- 不要让模型看到 API key、SSH key、kubeconfig、数据库 DSN。
- 不要把 Redis 当事实库。
- 不要在环境管理里直接给模型裸 shell。

## 2. 第一轮最应该做的代码任务

### 任务 A：Auth Principal + /me（本次补丁已做 MVP）

本次补丁已经新增：

```text
internal/platform/auth/principal.go
internal/platform/auth/middleware.go
server/adkrest/controllers/me.go
server/adkrest/internal/routers/me.go
```

并修改：

```text
internal/runtimeconfig/config.go
server/adkrest/handler.go
adk.yaml
```

后续需要补单测、前端读取 `/me`、以及 dev_token 模式的集成测试。

原始设计新增：

```text
internal/platform/auth/principal.go
internal/platform/auth/middleware.go
server/adkrest/controllers/me.go
server/adkrest/internal/routers/me.go
```

修改：

```text
internal/runtimeconfig/config.go
server/adkrest/handler.go
server/adkrest/internal/routers/routers.go
```

验收：

```bash
go test ./internal/platform/... ./server/adkrest/...
curl http://localhost:8080/api/me
curl -H "Authorization: Bearer $ADK_DEV_TOKEN" http://localhost:8080/api/me
```

### 任务 B：database session backend 暴露到 runtimeconfig（本次已做 SQLite MVP）

现有可复用代码：

```text
session/database/service.go
session/database/storage_session.go
session/database/session.go
```

本次已完成：

- `storage.session.type=sqlite`：已接 `github.com/glebarez/sqlite`；
- `storage.session.type=database`：已支持继承 `storage.database.*`；
- `dsn_env`：已支持；
- `auto_migrate`：已支持；
- DSN 和行为已写入 `docs/backend-platform-session-database.md`。

推荐配置：

```yaml
storage:
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true
  session:
    type: database
```

建议本地验证：

```bash
go test ./session/database ./internal/runtimeconfig
```

P1.2 已完成 PostgreSQL driver：`storage.session.type=database` + `storage.database.type=postgres` 会把 session/events 写入 PG。MySQL 仍是后续兼容项。

### 任务 C：runs + approvals 表和服务

新增：

```text
internal/platform/runs/model.go
internal/platform/runs/service.go
internal/platform/approvals/model.go
internal/platform/approvals/service.go
```

第一版可以先做 GORM 实现，不必马上挂到所有 runtime 事件。

验收：

- Create/List/Get run 单测；
- Create/Approve/Reject approval 单测；
- AutoMigrate 能建表。

## 3. 与现有代码的关键衔接点

### Runtime API

路径：

```text
server/adkrest/controllers/runtime.go
server/adkrest/internal/routers/runtime.go
```

后续在开始 run 时创建 `runs` 记录，在 SSE 中带上 run_id。

### Session API

路径：

```text
server/adkrest/controllers/sessions.go
server/adkrest/internal/routers/sessions.go
```

当前仍按 app/user/session 路径工作。平台化后需要从 Principal 检查 user scope，但先不要破坏现有调用。

### Skill API

路径：

```text
server/adkrest/controllers/skills.go
internal/skillservice
```

后续 registry 需要同时读 filesystem builtin 和 DB user skills。

### Metadata / Models

路径：

```text
server/adkrest/controllers/metadata.go
internal/runtimeconfig/config.go
```

后续 Model Registry 接入时，从 DB 读取 sanitized model metadata，再 fallback 到 YAML。

### EnvToolset

路径：

```text
tool/envmanagertool/toolset.go
tool/envmanagertool/service.go
tool/envmanagertool/catalog.go
tool/envmanagertool/types.go
```

后续从 EnvironmentService/SecretService/AuditService 读取，不再只读 `environments.example.json`。

## 4. 推荐 patch 切分

### Patch 1：文档 + 配置草案

包含本文档和后台设计文档，不动核心代码。

### Patch 2：Auth Principal

只做身份上下文和 `/me`，不做完整用户系统。

### Patch 3：SQLite database session

先接已有 `session/database`，让 runtimeconfig 支持 sqlite；P1.2 已补 PostgreSQL。

### Patch 4：Run/Approval Service

加表、service、单测、API 草案。

### Patch 5：Runtime 写 run 状态

把 runtime controller 和 runs service 接起来。

### Patch 6：环境管理服务化第一步

环境 CRUD + audit，不急着真实执行远程命令。

## 5. 风险点

| 风险 | 处理 |
| --- | --- |
| 一次性改太多导致不可运行 | 每个 patch 都必须能 `go test` |
| Auth 改造破坏本地开发 | 默认 `auth.mode=none` 或 dev fallback |
| DB 接入引入新 driver 导致依赖下载失败 | PostgreSQL 已引入 `gorm.io/driver/postgres`；如果内网无法下载，先配置 GOPROXY 或 vendoring |
| Run 暂停恢复过度设计 | 先 PG 保存状态 + Redis 保存短期 continuation |
| 环境管理误执行危险命令 | 默认 dry-run + allow_execute=false + approval |
| Secret 泄露 | 只保存 secret_ref，日志/trace/metadata 脱敏 |

## 6. 完成定义

第一阶段完成时，应满足：

- 本地 filesystem 模式还能跑；
- 有 `/me`；
- 有 platform Principal；
- session 可以选择 sqlite/postgres 生产后端；
- run 和 approval 有事实表；
- 文档能解释用户管理、session、memory、skill、模型、环境管理如何逐步接入；
- 后续开发者能按 TODO 一个 patch 一个 patch 推进。


## 7. P1.2 当前状态：PostgreSQL 主库存储

本轮新增：

```text
gorm.io/driver/postgres
internal/platform/store OpenGORM 支持 postgres
internal/platform/users
internal/platform/projects
server/adkrest/controllers/platform_users.go
server/adkrest/controllers/platform_projects.go
server/adkrest/internal/routers/platform_users.go
server/adkrest/internal/routers/platform_projects.go
```

当前 PG 会承载：

```text
tenants / users / roles / user_roles
projects / project_members
sessions / events / app_states / user_states
runs / run_steps
approval_requests
```

验证：

```powershell
$env:ADK_DATABASE_DSN="postgres://adk:adk@127.0.0.1:5432/adk?sslmode=disable"
go run .\cmd\internaldkcli
curl.exe http://localhost:8080/api/platform/tenant
curl.exe http://localhost:8080/api/platform/users
curl.exe -X POST http://localhost:8080/api/platform/projects -H "Content-Type: application/json" -d '{"name":"demo-project","app_name":"test1"}'
```
