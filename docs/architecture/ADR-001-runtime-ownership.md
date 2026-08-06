# ADR-001: AISphere Runtime 所有权与唯一 Agent Loop

- 状态：Accepted
- 日期：2026-08-06
- 适用仓库：`aisphere-agentkit`

## 背景

`aisphere-agentkit` 当前同时承担 ADK-Go 框架、AISphere Runtime 服务和第二套后台平台三种角色，并且与 Sandbox 中的 Python Session Worker 形成两套 Agent Loop。该结构会导致 Prompt、Skill、Tool、Model、Event、Approval 和错误语义长期分叉。

本 ADR 采用破坏性重构策略，明确 Runtime 是 AISphere 的执行核心，但不是第二个 Hub。

## 决策

`aisphere-agentkit` 收缩并重构为两层：

```text
ADK Core / SDK
  Agent、Runner、Tool、Session interface

AISphere Runtime
  Run Engine、Context Builder、Skill Loader、Tool Broker、Model Client、Event Ledger
```

生产环境中只允许一套 Agent Loop：

```text
AISphere Runtime / ADK-Go Runner
```

Sandbox 中的 Python Session Worker 不再作为生产 Agent Loop 路线继续发展。

## Runtime 拥有的对象

Runtime 是以下运行实例的唯一事实源：

- `Conversation`
- `Session`
- `Run`
- `ExecutionSnapshot`
- `ApprovalRequest`
- `ApprovalGrant`
- `ToolInvocation`
- `RuntimeEvent`
- Conversation Summary
- Memory Context 与 Memory Delta
- Run cancel / retry / resume 状态

## Runtime 的核心职责

Runtime 负责：

1. 从 Hub 获取已发布 `AgentRevision` 的不可变 `ExecutionSpec`。
2. 创建并持久化 `Run` 与 `ExecutionSnapshot`。
3. 编译 Agent、Model、Skills、Tools 和 Runtime Policy。
4. 构建每轮 `ModelContext`。
5. 运行唯一 ADK-Go Agent Loop。
6. 对 Tool Call 执行校验、授权、审批、凭据委派、路由和审计。
7. 调用 Model Gateway，而不是把模型凭据下发给 Sandbox。
8. 管理事件流、SSE、恢复、取消和重试。

## Runtime 不拥有的对象

以下定义资产仍由 Hub 管理：

- Agent Catalog / AgentRevision 发布
- Skill Registry / SkillVersion
- Tool Catalog / ToolVersion
- ModelProfile Catalog
- SandboxProfile Catalog
- 资产分享与发布治理

Runtime 只消费不可变引用与快照，不建设第二套管理后台。

## 唯一 Agent Loop

标准运行链路：

```text
User Message
  -> Runtime creates Run
  -> Hub resolves immutable ExecutionSpec
  -> Runtime persists ExecutionSnapshot
  -> Context Builder builds ModelContext
  -> ADK-Go Runner calls Model Gateway
  -> Tool Broker handles tool calls
  -> Sandbox / MCP / HTTP / Internal adapter
  -> Event Ledger
```

禁止生产链路：

```text
Runtime
  -> Sandbox Session Worker
  -> second Agent Loop
  -> Model Gateway
```

## Context Builder

Context Builder 是 Runtime 核心，而不是 Hub 或 Sandbox 功能。每轮模型请求由它组装：

```text
System Core
+ Agent Instruction
+ Relevant Memory
+ Recent Conversation
+ Active Skill Instructions
+ Retrieved References
+ Tool Schemas
+ Current User Message
```

完整 Skill 包、完整历史和完整文件不得无条件进入模型上下文。

## Skill 边界

Runtime 负责：

- 根据 ExecutionSnapshot 激活 Skill。
- 加载 `SKILL.md` 或入口指令。
- 按需检索 references。
- 将需要执行的脚本、模板和资产挂载到 Sandbox。

纯 Instruction 与 Reference 由 Runtime Context 管理；只有脚本、模板、二进制或 Tool 需要读取的资源才进入 Sandbox。

## Tool 边界

现有 `RuntimePlan`、`toolruntime.Registry` 和 `PermissionGate` 保留，但演进为：

```text
ToolSnapshot
  -> Tool Compiler
  -> ExecutableTool
  -> Unified Invocation Pipeline
  -> Adapter
```

所有 Sandbox、MCP、HTTP 和 Internal Tool 必须经过同一 Invocation Pipeline。

## 停止发展的方向

以下 AgentKit 平台化能力停止新增：

- 第二套用户/租户管理
- 第二套 Skill Registry
- 第二套 Model Registry
- Environment 管理后台
- 第二套 IAM
- 第二套对象存储控制面
- Sandbox 生命周期控制器

现有相关文档视为历史设计输入，后续由 Hub、Runtime、Sandbox 各自的 ADR 取代。

## 迁移原则

1. GoRunner 成为唯一生产 Agent Loop。
2. Python Session Worker 先标记 deprecated，再移除模型调用与 Prompt 组装能力。
3. Session Worker 需要保留的文件/进程能力迁移到 Sandbox Tool Server。
4. `approvalConfirmed`、`approvedTools` 等客户端布尔值迁移为服务端持久化 ApprovalGrant。
5. Session 内存中的 RuntimePlan 迁移为持久化 Run/ExecutionSnapshot。
6. 旧路径不得长期双写或自动 fallback。

## 成功标准

- 同一个 Run 只有一个 Agent Loop 和一个 Event Ledger。
- Runtime 重启后可以用持久化 ExecutionSnapshot 恢复。
- Sandbox 不调用模型，也不装配 Prompt。
- Runtime 不管理 Hub 资产 Catalog。
- 所有 Tool Invocation 都能关联 runId、snapshotId、approvalId 和 traceId。
