# AISphere 系统职责边界与协作契约

> 状态：Current Architecture Contract  
> 日期：2026-08-07  
> 适用范围：Hub / Runtime(AgentKit) / Sandbox / IAM / Model Gateway / Console

本文不是需求清单，而是 AISphere 各组件的**所有权约束**。任何新代码如果无法明确归属到下述某个 Owner，应先补 ADR，而不是在多个组件重复实现。

## 1. 核心原则

### 1.1 一个事实只有一个 Owner

同一类事实不得被两个组件长期双写或各自维护状态机。

```text
Definition / Catalog facts -> Hub
Execution facts            -> Runtime
Authorization facts        -> IAM
Isolation / lease facts    -> Sandbox
Model routing facts        -> Model Gateway
```

缓存、SSE buffer、前端状态、Session 临时状态都不能升级成第二事实源。

### 1.2 Control Plane 与 Execution Plane 分离

```text
Hub      = Control Plane
Runtime  = Execution Plane
Sandbox  = Isolation Plane
IAM      = Authorization Authority
```

Hub 决定“可以发布什么”；Runtime 决定“一次 Run 实际执行了什么”；Sandbox 只负责“在哪里隔离执行”；IAM 决定“谁被允许做什么”。

### 1.3 Runtime 是唯一 Agent Loop

生产环境只允许：

```text
Runtime / ADK-Go Runner
```

禁止 Sandbox Worker、Hub 或前端再维护第二套 Prompt 装配、模型调用、Tool loop 或 Run 状态机。

## 2. 组件职责矩阵

| 组件 | 必须负责 | 明确不负责 |
| --- | --- | --- |
| Hub | Agent/Skill/Tool/ModelProfile/SandboxProfile Catalog；版本；发布；分享；ExecutionSpec resolve | Run 生命周期、模型调用、Tool 实际执行、Sandbox lease、运行审批状态机 |
| Runtime / AgentKit | Run、ExecutionSnapshot、Attempt、Event Ledger；Context Builder；唯一 Agent Loop；Tool Compiler/Broker；Model 调用；恢复/重试 | 第二套 Catalog、第二套 IAM、环境资产后台、长期 Secret 管理 |
| Sandbox | Workspace、Shell、Browser、Interpreter、隔离进程；Lease；受控 Tool Server | 用户权限决策、Prompt 组装、模型调用、Catalog、长期 Credential |
| IAM | Principal、ReBAC/Policy decision、资源权限、授权审计 | Run 状态、Tool adapter、Sandbox 生命周期、Agent 配置发布 |
| Model Gateway | Provider credential、模型路由、协议适配、限流/计量、健康检查 | Agent 编排、Prompt 业务逻辑、Tool policy、Run 状态 |
| Console / Frontend | 管理与观察 UI；提交用户意图；展示审批、Run、Event | 产生可信 Principal、产生最终授权结果、篡改 Runtime execution facts |

## 3. 关键对象唯一归属

| 对象 | Owner | 消费者 |
| --- | --- | --- |
| Agent / AgentRevision | Hub | Runtime |
| Skill / SkillVersion | Hub | Runtime / Sandbox(资源挂载) |
| Tool / ToolVersion / ToolProvider | Hub | Runtime |
| ToolConnection metadata | Hub | Runtime Credential Broker |
| ModelProfile | Hub | Runtime / Model Gateway |
| SandboxProfile | Hub | Runtime / Sandbox |
| Principal / Permission Decision | IAM | Hub / Runtime / Sandbox Gateway |
| Conversation / Session | Runtime | Console |
| Run | Runtime | Console / Audit |
| ExecutionSnapshot | Runtime | Runtime / Debug / Audit |
| RunAttempt | Runtime | Runtime / Ops |
| RuntimeEvent | Runtime | SSE / Console / Audit |
| ApprovalRequest / ApprovalGrant | Runtime（流程事实）+ IAM（授权约束） | Tool Broker / Console |
| ToolInvocation | Runtime | Audit / Analytics |
| SandboxLease | Sandbox | Runtime |
| Provider credential | Credential/Vault/Model Gateway | Runtime 仅使用短期引用 |

## 4. 标准执行链路

```text
User / Console
    |
    v
Gateway / OIDC
    |
    v
Runtime
    |-- 1. Hub: resolve published AgentRevision -> immutable ExecutionSpec
    |-- 2. create Run
    |-- 3. persist immutable ExecutionSnapshot
    |-- 4. create RunAttempt
    |-- 5. compile Context / Model / Skill / Tool
    |-- 6. ADK-Go Agent Loop
    |       |-- Model Gateway
    |       |-- Tool Invocation Pipeline
    |               |-- IAM / Policy
    |               |-- Approval
    |               |-- Credential Broker
    |               |-- Sandbox / MCP / HTTP / Internal Adapter
    |
    |-- 7. append RuntimeEvent before SSE projection
    `-- 8. atomically finalize Attempt + Run + terminal events
```

任何绕过该链路直接从 Agent/Sandbox 调用外部高权限能力的实现，都视为架构违规。

## 5. Runtime 内部模块清单

### `internal/aihubruntime`

负责：Hub Runtime API client、ExecutionSpec/Plan 获取与传输适配。

禁止：缓存成第二套 Catalog、修改 Hub 发布资产。

### `internal/sessionnative`

负责：把 Runtime Session 与 Sandbox lease、Hub plan 建立运行时关联；提供 Runtime 所需的 Tool/Skill 运行资源。

禁止：运行第二 Agent Loop、调用模型、持久化 Catalog。

### `internal/runtimeexecutor`

负责：基于已编译 Plan 驱动 ADK-Go Agent Loop。

禁止：自行 resolve “latest” 版本、创建第二套 Run 状态。

### `internal/platform/runs`

负责：唯一执行事实 Store：

```text
Run
ExecutionSnapshot
RunAttempt
RuntimeEvent
```

特性：版本化 PostgreSQL migration、状态机、幂等、append-only Event Ledger、终态事务。

禁止：`run_steps`、第二 pgx Store、浏览器写执行事实。

### `internal/modelruntime`

负责：把 Snapshot/Plan 中已确定的模型配置编译成 Runtime model client。

目标演进：只通过 Model Gateway/统一 provider client 获取模型能力，不接收前端明文密钥。

### Tool Runtime / Registry

负责：

```text
ToolSnapshot
 -> Validator
 -> Compiler
 -> ExecutableTool
 -> Invocation Pipeline
 -> Adapter
```

Adapter 首批固定为：`sandbox / mcp / http / internal`。

禁止：缺失 adapter 时默认 internal；禁止客户端布尔值直接成为授权结果。

### `internal/runtimetrace`

负责：运行期 trace/diagnostic projection。

RuntimeEvent 才是 Run 时间线事实；trace 不替代 Event Ledger。

### `server/adkrest`

负责：HTTP/SSE/WebSocket transport、认证上下文接入、只读运行查询入口、执行入口。

禁止：Controller 内维护长期 Run 状态；禁止再暴露 `run_steps` 写接口。

## 6. Tool 调用协作契约

所有 Tool，无论来自 Sandbox、MCP、HTTP 还是 Internal，都经过统一 pipeline：

```text
Snapshot allowlist
 -> input schema validation
 -> argument/resource policy
 -> IAM authorization
 -> risk evaluation
 -> approval
 -> rate/concurrency control
 -> short-lived credential resolution
 -> adapter invoke
 -> output schema/size validation
 -> redaction
 -> RuntimeEvent + ToolInvocation audit
```

Tool 的定义、连接、策略、部署信息来自 Hub 发布快照；Runtime 不在执行中重新选择 latest 版本。

## 7. Sandbox 协作契约

Runtime 可以向 Sandbox 请求：

- 创建/复用 lease；
- 挂载 Skill 资源和用户 workspace；
- 执行 shell/python/browser/workspace 类 Tool；
- 获取执行输出。

Runtime 不向 Sandbox 下发：

- 长期 OAuth token/API key；
- IAM 管理权限；
- 模型 provider secret；
- 可自行扩大权限的 policy。

Sandbox 不对“用户是否允许此次操作”做最终判断；最终授权在 Runtime Invocation Pipeline + IAM。

## 8. IAM 协作契约

入口身份由 Gateway/OIDC 建立可信 Principal。内部服务只接受可信身份上下文，不信任浏览器伪造的身份 Header。

Runtime 在执行敏感操作时按资源维度请求授权，例如：

```text
tool.execute
connection.use
sandbox.create
repository.issue.create
secret.delegate
```

Approval 不是 IAM 的替代品：IAM 判断“有没有资格”，Approval 判断“本次具体副作用是否已被确认”。

## 9. 明确停止发展的能力

AgentKit 中以下方向停止新增并逐步删除：

- 第二套 User/Tenant/IAM 控制面；
- 第二套 Skill/Model/Tool Catalog；
- `run_steps`；
- pgx `platform_runs/platform_run_events` 第二 Store；
- Redis resumable buffer 作为 Run 历史事实源；
- Sandbox Python Session Worker Agent Loop；
- Controller 内存 Run 状态；
- 客户端 `approvalConfirmed/approvedTools` 作为最终授权；
- Runtime 内 Environment 管理后台。

## 10. 架构验收不变量

任何 PR 合入前至少检查：

1. 是否新增了第二事实源？
2. 是否出现同一业务状态的双写？
3. 是否让 Runtime 在执行时重新选择 latest 版本？
4. 是否让 Sandbox 获取长期 Credential 或权限决策权？
5. 是否让前端字段变成可信授权结果？
6. 是否有 Tool 绕开统一 Invocation Pipeline？
7. 是否有模型调用绕开唯一 ADK-Go Agent Loop？
8. Run 是否能从 PostgreSQL Snapshot/Event Ledger 解释和重放？

任何一项答案为“是”，默认阻止合入，除非新增 ADR 明确改变系统边界。

## 11. 当前收口顺序

```text
P0  Runtime execution facts / legacy run cleanup
P1  Tool Compiler + unified Invocation Pipeline
P1  server-side ApprovalGrant + Credential Broker
P1  Hub immutable AgentRevision / ToolVersion pinning
P2  Sandbox Tool Server contract cleanup
P2  MCP discovery/schema-drift lifecycle
P2  Model Gateway integration
P3  Memory / Context pipeline standardization
```

目标不是把所有功能堆进 AgentKit，而是让每个组件都只做自己拥有的事情，并通过稳定契约协同。
