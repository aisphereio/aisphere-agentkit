# ADR-002: Runtime 执行事实模型

- 状态：Proposed
- 日期：2026-08-06
- 适用仓库：`aisphere-agentkit`
- 依赖：ADR-001

## 背景

现有 Runtime 同时存在：

- Controller 内临时运行状态；
- Redis resumable run buffer；
- GORM `runs/run_steps`；
- pgx `platform_runs/platform_run_events`；
- ADK Session Event。

这些对象的 ID、状态和表结构并不统一，无法回答“一次执行究竟使用了哪一版 Agent、Model、Skill、Tool 和 Policy”，也不能在 Runtime 重启后可靠重建运行事实。

## 决策

Runtime 使用以下四个核心事实对象：

```text
Run
  ├── ExecutionSnapshot（唯一、不可变）
  ├── RunAttempt 1..N
  └── RuntimeEvent 1..N（append-only）
```

只有 Runtime 执行引擎可以创建和修改这些事实。Console/API 仅提供查询和事件订阅，不提供创建 Snapshot、Attempt、Event 或修改 Run 状态的公开写接口。

### Run

表示用户发起的逻辑任务。Retry 不修改 Snapshot，而是创建新的 Attempt。

同一租户内使用 `idempotency_key` 防止网络重放创建重复 Run。已经处于 queued、running、waiting_approval 或成功终态的 Run 不允许创建新的 Attempt。

### ExecutionSnapshot

表示本次 Run 的不可变执行契约。必须在模型第一次调用之前持久化。

Snapshot 包含：

- Hub ExecutionSpec 的精确版本引用；
- Agent、Model、Skill、Tool、SandboxProfile 与 Policy revision；
- Principal、Tenant、Project 等引用；
- Runtime 支持的 schema version 和 resolver/compiler version；
- 本次输入与附件引用；
- source spec digest 和 snapshot digest。

Snapshot 不得包含：

- API Key、OAuth Token、Password、Client Secret；
- Sandbox Pod IP 或动态 endpoint；
- Sandbox Lease；
- 临时 delegated credential；
- 把当前 IAM 角色复制成永久授权结果。

Snapshot Repository 不提供 Update 操作。

敏感字段识别需覆盖 snake_case、kebab-case 和 camelCase，但不得把 Tool JSON Schema 中名为 `token`、`password` 的属性定义误判为凭据值。

### RunAttempt

表示一次物理执行。保存 Runtime build、compiler version、compiled plan digest、Sandbox lease reference 与失败信息。

普通重试复用原 Snapshot 并增加 Attempt；配置发生变化时必须创建新的 Run。

### RuntimeEvent

RuntimeEvent 是追加写事实，使用 `(run_id, sequence)` 唯一约束。SSE、审计时间线和调试界面都从 Event Ledger 投影，不再把 Controller 内存或 SSE 连接作为事实源。

ADK Event 必须先写入 Event Ledger，成功取得 sequence 后才能向客户端投影。

### 终态事务

Attempt 终态、Run 终态和两条 terminal events 必须在同一个数据库事务中提交：

```text
Attempt.status
+ Run.status
+ attempt.completed|failed|cancelled
+ run.completed|failed|cancelled
```

不允许出现 Run 已成功但 `run.completed` 缺失的半状态。

## 状态机

Run：

```text
preparing -> queued -> running -> succeeded
                         |  |
                         |  +-> waiting_approval -> running
                         +----> failed / cancelled
```

Attempt：

```text
queued -> running -> succeeded
             |  |
             |  +-> waiting_approval -> running
             +----> failed / cancelled
```

终态记录不可回退。需要重试时创建新 Attempt。

## 持久化

目标单一实现：

```text
GORM + PostgreSQL
```

表：

```text
runtime_runs
runtime_execution_snapshots
runtime_run_attempts
runtime_events
```

旧的 pgx `platform_runs/platform_run_events` 是重复事实源。Runtime 启动链已经统一使用 GORM Store；旧 pgx 实现和 `run_steps` 将作为后续删除项，不允许新增调用或双写。

## 查询与 SSE

只读查询：

```http
GET /platform/runs/{runId}
GET /platform/runs/{runId}/snapshot
GET /platform/runs/{runId}/attempts
GET /platform/runs/{runId}/events?after=128
```

可恢复 SSE：

```http
GET /platform/runs/{runId}/events/stream?after=128
Last-Event-ID: 128
```

SSE `id` 等于 RuntimeEvent.sequence。客户端断线后从最后确认的 sequence 继续读取；连接跨 Runtime 重启仍可恢复。Redis 只能作为短期加速层或 continuation buffer，不能成为历史事实源。

公开路由不再注册：

```text
POST /platform/runs
PATCH /platform/runs/{runId}
POST/PATCH run_steps
```

## 摘要

ExecutionSnapshot 使用规范化 JSON 计算 SHA-256：

```text
parse one JSON value
-> preserve JSON numbers
-> reject credential-value fields
-> deterministic marshal
-> sha256
```

相同语义、不同 key 顺序必须得到相同 digest。不同 Run 可以拥有 digest 相同但记录独立的 Snapshot。

## 迁移顺序

1. 建立事实模型、状态机和单元测试。已完成。
2. 将 Runtime 启动链统一到 GORM Store。已完成。
3. Native ADK-Go 在模型调用前创建 Run、Snapshot 和 Attempt。已完成。
4. ADK Event 映射并追加 RuntimeEvent。已完成。
5. SSE 改为 Event Ledger 投影与重放。已完成第一版。
6. 增加显式 PostgreSQL DDL migration，替代生产 AutoMigrate。
7. 删除旧 pgx Run Store、`run_steps` 和 Controller 兼容代码。
8. 退役或迁移非 Native 旧 `/run` 执行路径。

## 验收标准

- 一个 Run 只能绑定一个不可变 Snapshot。
- Snapshot 在模型请求之前落库。
- 每次合法 Retry 都创建新的 Attempt；活跃或成功 Run 不会被重复执行。
- Attempt/Run 终态与 terminal events 原子提交。
- RuntimeEvent sequence 单调递增并可用于 SSE 重放。
- Runtime 重启后仍能查询 Run、Snapshot、Attempt 和 Event。
- Snapshot 中不存在 credential value。
- 不支持的 schema version 必须失败关闭。
- 浏览器不能创建或篡改 Runtime 执行事实。
