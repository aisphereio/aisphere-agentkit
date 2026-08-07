# AISphere Runtime Architecture

本目录是 `aisphere-agentkit` 在 AISphere 平台中的**唯一有效架构入口**。旧平台化设计、Session Worker 双 Agent Loop、legacy Run Store 等文档不再作为当前实现依据。

## 当前有效文档

- [SYSTEM-BOUNDARIES.md](SYSTEM-BOUNDARIES.md) — AISphere 跨组件职责边界、对象 Owner 与协作契约。
- [MODULE-CATALOG.md](MODULE-CATALOG.md) — Hub / Runtime / Sandbox / IAM / Model Gateway 的模块功能清单、接口和架构债务台账。
- [STORAGE-RETRIEVAL-RESERVATION.md](STORAGE-RETRIEVAL-RESERVATION.md) — Conversation / File / Memory / Retrieval 的逻辑位置与未来 OceanBase adapter 预留；当前不实现临时存储后端。
- [TOOL-INVENTORY-V1.md](TOOL-INVENTORY-V1.md) — 当前 `configurable/tool/*` 能力逐项分类与迁移台账；Builtin V1 第一批实现及冲突 Tool 退役已进入代码阶段。
- [ADR-001: AISphere Runtime 所有权与唯一 Agent Loop](ADR-001-runtime-ownership.md) — Runtime 只保留一套 ADK-Go Agent Loop。
- [ADR-002: Runtime 执行事实模型](ADR-002-runtime-execution-facts.md) — `Run + ExecutionSnapshot + RunAttempt + RuntimeEvent` 唯一事实模型。
- [ADR-003: Tool Contract 与统一 Invocation Pipeline V1](ADR-003-tool-contract-v1.md) — Tool V1 总体分层与统一执行链，目前为 Proposed，继续随代码校准。
- [ADR-004: Builtin Tool V1](ADR-004-builtin-tools-v1.md) — Runtime code-first Builtin、Hub catalog mirror、Agent 显式选择及 V1 无独立 Builtin AuthZ 规则。

## 解释优先级

发生冲突时按以下顺序解释：

```text
Accepted ADR
  > SYSTEM-BOUNDARIES.md
  > MODULE-CATALOG.md
  > 当前代码与 API contract
  > 其他说明文档
  > Git 历史中的旧设计
```

如果当前代码与 Accepted ADR 冲突，应修代码或新增 ADR；不能用兼容分支长期掩盖冲突。

## AgentKit 当前定位

```text
AgentKit
├── ADK Core / SDK
└── AISphere Runtime
    ├── Run Engine
    ├── ExecutionSnapshot
    ├── Context Builder
    ├── ADK-Go Agent Loop
    ├── Tool Compiler / Broker
    ├── Model Runtime
    ├── Approval / Credential coordination
    ├── Conversation / File / Memory / Retrieval ports (reserved)
    └── RuntimeEvent Ledger
```

AgentKit **不是**：

- 第二个 Hub；
- 第二套 IAM；
- 第二套 Skill/Tool/Model Registry；
- Sandbox 控制面；
- Environment 管理后台；
- 前端管理系统。

## 当前运行事实

Runtime 唯一执行事实：

```text
Run
  ├── ExecutionSnapshot 1:1 immutable
  ├── RunAttempt       1:N
  └── RuntimeEvent     1:N append-only
```

标准只读接口：

```text
GET /platform/runs
GET /platform/runs/{run_id}
GET /platform/runs/{run_id}/snapshot
GET /platform/runs/{run_id}/attempts
GET /platform/runs/{run_id}/events
GET /platform/runs/{run_id}/events/stream
```

Conversation / File / Memory 与 Run facts 分离：

```text
Conversation / Message / Memory / Knowledge
  = 可持续 Context 与用户/项目知识

Run / Snapshot / Attempt / RuntimeEvent
  = 一次执行的事实与审计时间线
```

已废弃并从目标架构删除：

```text
run_steps
platform_runs / platform_run_steps / platform_run_events pgx Store
公开 Run 状态写接口
Sandbox Session Worker 第二 Agent Loop
Redis resumable buffer 作为历史事实源
```

## 文档治理规则

新增架构文档前先判断：

1. 是跨组件长期边界？更新 `SYSTEM-BOUNDARIES.md`。
2. 是模块职责、依赖或债务台账？更新 `MODULE-CATALOG.md`。
3. 是不可逆/高影响技术决策？新增 ADR。
4. 是实现说明或运维指南？放普通 docs，但不得重新定义 Owner。
5. 已被 Accepted ADR 否定的设计直接删除，Git history 即历史档案，不在主分支保留“可能有效”的冲突文档。

## 当前开发顺序

```text
1. Runtime legacy 执行链收口
2. BuiltinRegistry + Tool Compiler + Unified Invocation Pipeline
3. server-side ApprovalGrant + Credential Broker
4. Hub immutable revision/version pinning
5. Sandbox Tool Server / Lease contract
6. MCP discovery/schema drift
7. Model Gateway integration
8. Context Builder skeleton + Conversation/File/Memory/Retrieval ports
9. OceanBase adapter / indexing / hybrid retrieval
10. Conversation / Knowledge / Memory product APIs
```

第 8 步以前不为 Conversation、File/Knowledge 或 Memory 建设临时 PostgreSQL/MinIO/Vector Store 数据面。每一步都必须满足 `SYSTEM-BOUNDARIES.md` 中的架构不变量，并同步维护 `MODULE-CATALOG.md` 的债务状态。