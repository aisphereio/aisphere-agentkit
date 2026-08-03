# 后台平台长任务推进计划

> 这份文档用于后续持续开发。原则：先把底座打稳，再逐步服务化 Skill、模型、环境、Memory。每一阶段都要能独立编译、运行、回滚。

## 0. 开发原则

1. **小步可运行。** 每个 patch 控制在一个明确目标内，避免一次性重构过大。
2. **接口先稳定。** DB 表、Service interface、HTTP API 命名先定住，具体实现可替换。
3. **兼容现有 filesystem 模式。** 本地开发不能因为平台化而变重。
4. **生产能力以 PostgreSQL + Redis + MinIO 为主。** MySQL 作为兼容项，不作为首选设计中心。
5. **任何 Secret 都不能进日志、trace、memory、prompt。**
6. **环境操作默认 dry-run。** 真实执行必须经过 allow_execute、risk、safety、approval 多重门。

## P0：平台底座与身份上下文

目标：所有请求都有 Principal，所有后台模块有统一 DB/migration/config 入口。

### P0.1 新增 platform 包骨架（本次已开始）

本次补丁已经新增 `internal/platform/auth`，用于承载 Principal 和 REST middleware。后续继续补 `store/users/runs/approvals`。

目标新增目录：

```text
internal/platform/
  auth/
    principal.go
    middleware.go
  store/
    db.go
    migrate.go
  users/
    model.go
    service.go
  runs/
    model.go
    service.go
  approvals/
    model.go
    service.go
```

验收：

- `go test ./internal/platform/...` 通过；
- 不影响现有 filesystem 模式；
- 没有引入必须配置数据库才能启动的问题。

### P0.2 Auth Principal（本次已做 MVP）

本次补丁已实现：

- `runtimeconfig.AuthConfig` / `DevTokenConfig`；
- `auth.mode=none` 默认本地模式；
- `auth.mode=dev_token` Bearer token 校验；
- request context 注入 `Principal`；
- `GET /me` 返回当前身份。

核心结构：

```go
type Principal struct {
    TenantID string
    UserID   string
    Roles    []string
    Scopes   []string
}
```

支持配置：

```yaml
auth:
  mode: dev_token
  dev_tokens:
    - token_env: ADK_DEV_TOKEN
      tenant_id: default
      user_id: admin
      roles: [owner]
      scopes: ["*"]
```

HTTP middleware 行为：

- `auth.mode=none`：注入 default/admin，仅限本地开发；
- `auth.mode=dev_token`：校验 Bearer token；
- 无 token 或 token 错误返回 401。

验收：

- `GET /me` 能返回 Principal；
- 现有 API 在 `auth.mode=none` 下不破坏；
- `dev_token` 下无 token 被拒绝。

### P0.3 Storage Config 扩展（本次已做 SQLite Session MVP）

当前 `runtimeconfig.ServiceConfig` 已有 `Type/Root/DSN/Opts`，本次补丁补充了 `DSNEnv/AutoMigrate` 和共享的 `StorageConfig.Database`。

建议配置：

```yaml
storage:
  database:
    type: postgres
    dsn_env: ADK_DATABASE_DSN
  session:
    type: database
```

本次已完成：

- 为 `StorageConfig` 增加 `Database DatabaseConfig`；
- 为 `DatabaseConfig/ServiceConfig` 增加 `dsn_env` 和 `auto_migrate`；
- 支持 `storage.session.type=sqlite`；
- 支持 `storage.session.type=database` 继承 `storage.database.*`；
- 复用 `session/database.NewSessionService` 和 `session/database.AutoMigrate`；
- SQLite 已支持；
- PostgreSQL 已在 P1.2 支持；
- MySQL 仍是后续兼容项，当前 fail fast。

验收：

- filesystem 默认不变；
- sqlite session 后端可创建、读取 session；
- PostgreSQL session 后端可通过 `ADK_DATABASE_DSN` 连接；
- `storage.session.type=database` 可以复用 `storage.database`。

### P0.4 Migration 入口

新增 CLI：

```bash
go run ./cmd/adkgo migrate
```

或先用启动时配置：

```yaml
storage:
  database:
    auto_migrate: true
```

验收：

- session database tables 可自动创建；
- platform users/runs/approvals 表可创建；
- migration 日志清晰。

## P1：Run / Approval 稳定化

目标：刷新浏览器、暂停、审批、恢复不再只依赖前端内存和 SSE。

### P1.1 Run Store

新增 `runs.Service`：

```go
type Service interface {
    Create(ctx context.Context, req CreateRunRequest) (*Run, error)
    MarkRunning(ctx context.Context, runID string) error
    MarkWaitingApproval(ctx context.Context, runID, approvalID string) error
    MarkCompleted(ctx context.Context, runID string) error
    MarkFailed(ctx context.Context, runID string, err error) error
    Get(ctx context.Context, runID string) (*Run, error)
    List(ctx context.Context, filter ListRunsFilter) ([]*Run, error)
}
```

接入点：

- `server/adkrest/controllers/runtime.go` 创建 run；
- SSE event 中输出 `run_id`；
- 结束时写 completed/failed/cancelled。

验收：

- 前端刷新后能通过 `/runs/{id}` 查到状态；
- failed run 有错误信息；
- run 关联 session_id/app_name/user_id。

### P1.2 Approval Store

新增 `approvals.Service`：

```go
type Service interface {
    Create(ctx context.Context, req CreateApprovalRequest) (*ApprovalRequest, error)
    Approve(ctx context.Context, id string, decision Decision) error
    Reject(ctx context.Context, id string, decision Decision) error
    Get(ctx context.Context, id string) (*ApprovalRequest, error)
    ListPending(ctx context.Context, filter Filter) ([]*ApprovalRequest, error)
}
```

接入点：

- Tool confirmation；
- user choice；
- EnvToolset high-risk operation；
- 后续 artifact review。

验收：

- pending approval 可查询；
- approve/reject 可持久化；
- run 状态能进入 `waiting_approval`。

### P1.3 Redis 与 PG 分工

Redis：

```text
adk:run:{run_id}:stream       SSE stream buffer
adk:run:{run_id}:continuation 运行态 continuation
adk:approval:{id}:token       短期确认 token
```

PG：

```text
runs
run_steps
approval_requests
```

注意：Redis key 前缀不要写错，建议统一从配置读取 `server.api.resumable_runs.key_prefix`。

验收：

- Redis 丢失不影响历史 run 查询；
- PG 丢失才算事实丢失；
- run continuation 过期后返回明确错误：`run expired, please restart from latest session state`。


## P1.2 已完成：PostgreSQL Storage MVP

本轮已把平台事实库推进到 PostgreSQL-first：

```text
GORM + gorm.io/driver/postgres + pgx
```

已落地：

```text
storage.database.type=postgres
storage.database.dsn_env=ADK_DATABASE_DSN
storage.database.max_open_conns / max_idle_conns / conn_max_lifetime / conn_max_idle_time
storage.session.type=database 继承 PostgreSQL
platform users / tenants / roles
platform projects / project_members
platform runs / run_steps
platform approvals
```

验收：

```bash
go test ./internal/runtimeconfig ./internal/platform/store ./internal/platform/users ./internal/platform/projects ./internal/platform/runs ./internal/platform/approvals
```

启动后验证：

```bash
curl http://localhost:8080/api/platform/tenant
curl http://localhost:8080/api/platform/users
curl http://localhost:8080/api/platform/projects
```

下一步不是继续扩数据库 driver，而是接真实 runtime：`/run_sse` 创建 run，SSE 首帧返回 run_id，结束时更新 run 状态。

## P2：Skill Registry 服务化

目标：内置 Skill 和用户 Skill 统一查询，后续前端可上传和版本化。

任务：

1. 新增 `internal/platform/skills` registry；
2. filesystem source 作为 builtin source；
3. DB source 作为 user source；
4. MinIO/S3 保存 `SKILL.md` 和 resources；
5. `/skills` API 聚合 builtin + user；
6. Agent Loader 通过 registry resolve skills。

验收：

- 当前 `skills/` 下 Skill 仍能加载；
- 新增 Skill version publish 后能被 Agent 引用；
- 禁用 Skill 后 Agent 加载失败并给出明确错误；
- Skill ID 不带业务路径前缀，保持稳定。

## P3：Model Registry 服务化

目标：模型配置不再只能写在 `adk.yaml`。

任务：

1. 新增 provider/spec/alias/credential 表；
2. 模型密钥改为 `secret_ref`；
3. `runtimeconfig.ResolveModelSpec` 支持 DB + YAML fallback；
4. `/models/specs/{id}/test` 调用轻量请求检测连通性；
5. 前端 builder 默认模型从 registry 读取。

验收：

- `/models` 不返回明文 key；
- 可以通过 API 新增 openai-compatible 模型；
- alias `default` 可切换到新模型；
- YAML-only 模式仍可运行。

## P4：Environment Store / Secret Store / Audit Store

目标：EnvToolset 不再只读 JSON，环境资产进入后台管理。

任务：

1. 新增 EnvironmentService；
2. 新增 SecretService，第一版可以本地加密，后续接 Vault/KMS；
3. 新增 OperationCatalogService；
4. 新增 AuditService；
5. EnvToolset Config 增加 `source: file|service`；
6. 执行结果大输出写 MinIO，DB 只存 object key。

验收：

- `/environments` 可 CRUD；
- Agent 调 env_list_environments 不暴露 secret；
- 高风险操作产生 approval；
- 审计日志可查到 command preview、risk、approved、output_object_key；
- 默认 dry-run，不会误执行。

## P5：Memory 服务化

目标：长期记忆可控、可编辑、可检索。

任务：

1. 新增 memory_entries/memory_chunks；
2. memory.Service 增加 DB implementation 或 platform memory facade；
3. `/memory` 管理接口；
4. session -> memory delta 提取；
5. 后续接 pgvector。

验收：

- 用户能看到、编辑、删除 memory；
- Agent 可按 app/user 检索 memory；
- 从 session 生成的 memory 带 source_session_id；
- 可关闭自动写 memory。

## P6：Artifact / Object 生产化

目标：产物、上传、trace、环境输出统一对象存储。

任务：

1. ObjectService 抽象；
2. MinIO/S3 backend；
3. Artifact metadata index；
4. signed URL / download endpoint；
5. 大型 trace 写 object。

验收：

- filesystem artifact 兼容；
- MinIO artifact 可保存/读取；
- 前端下载 artifact 不暴露 MinIO secret；
- artifact version 可查询。

## 推荐开发顺序

```text
第 1 个 patch：docs + config schema 草案 + platform 包空骨架
第 2 个 patch：auth Principal + /me + middleware
第 3 个 patch：sqlite/postgres session backend 接入 runtimeconfig
第 4 个 patch：runs/approvals model + service + API
第 5 个 patch：runtime controller 写 run 状态 + SSE 输出 run_id
第 6 个 patch：EnvToolset approval/audit 服务化接入
第 7 个 patch：Skill Registry DB+Object
第 8 个 patch：Model Registry DB+SecretRef
第 9 个 patch：Memory DB facade
第 10 个 patch：MinIO Artifact/ObjectService
```

## 每个 patch 的提交说明模板

```text
feat(platform): add auth principal and /me endpoint

- add internal/platform/auth Principal and middleware
- add auth config mode none/dev_token
- add /me endpoint for frontend bootstrap
- keep filesystem local mode unchanged

Test:
- go test ./internal/platform/... ./server/adkrest/...
- manual: curl /api/me with and without token
```

## P3 Agent 自提升治理闭环

在 Skill/User/组织等商业化后台之前，优先补齐基于真实运行现场的 Agent 改进闭环。

### P3.1 角色和治理 Agent

- [x] 为现有 Agent 增加 `metadata.role`。
- [x] 新增 `agent_ops` 平台治理入口。
- [x] 新增 `objective_review_agent`：客观审查。
- [x] 新增 `self_improvement_agent`：改进提案和 patch draft。
- [x] 新增 `approval_packet_agent`：人类审批包。
- [x] 新增相关 Skill：客观审查、自提升提案、审批包。

### P3.2 基于 run 的上下文包

- [ ] 从 run_id 收集 trace、run_steps、artifact、Agent YAML、Skill、Tool 调用。
- [ ] 输出 improvement context bundle。
- [ ] 支持用户反馈和上下文包合并。

### P3.3 改进提案 API

- [ ] `POST /api/platform/runs/{run_id}/improvement-context`
- [ ] `POST /api/platform/improvement-proposals`
- [ ] `GET /api/platform/improvement-proposals/{id}`
- [ ] `POST /api/platform/improvement-proposals/{id}/approve`
- [ ] `POST /api/platform/improvement-proposals/{id}/reject`

### P3.4 人类审批和应用

- [ ] 审批页面展示证据、diff、影响范围、风险和回滚。
- [ ] 第一版只下载/复制 patch。
- [ ] 第二版支持批准后由 Apply Service 应用 patch。
- [ ] 所有应用动作写 audit log。
