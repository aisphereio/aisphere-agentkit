# AISphere 模块功能清单与协作台账

> 状态：Current Engineering Map  
> 日期：2026-08-07  
> 依赖：`SYSTEM-BOUNDARIES.md`、ADR-001、ADR-002

本文用于回答三个工程问题：

1. 一个功能应该放在哪个组件？
2. 组件之间通过什么稳定契约协作？
3. 当前还有哪些重复能力需要删除或迁移？

原则：**按事实所有权拆模块，不按页面或技术栈拆模块。**

---

## 1. 一级组件地图

| 组件 | 类型 | 核心事实 | 主要职责 | 禁止职责 |
| --- | --- | --- | --- | --- |
| `aisphere-hub` | Control Plane | Agent/Skill/Tool/ModelProfile/SandboxProfile 的定义、版本、发布关系 | Catalog、Version、Publish、Share、Resolve | Run 状态、模型执行、Tool 数据面、Sandbox 生命周期、IAM 目录 |
| `aisphere-agentkit` | Runtime / Execution Plane | Conversation、Session、Run、Snapshot、Attempt、RuntimeEvent、ToolInvocation、Approval flow | Context、Agent Loop、Tool Broker、Model 调用、恢复/重试 | 第二套 Catalog、第二套 IAM、Environment 后台、Sandbox Controller |
| `aisphere-sandbox` | Isolation Plane | SandboxLease、Workspace/compute lifecycle、infra status | Profile infra mapping、Lease、Quota、Executor、Network/Resource isolation | Agent Loop、Model、Prompt、业务授权、长期 Credential |
| `aisphere-iam` | Identity & Authorization Plane | Directory/control facts、Project/Grant、authorization projection | Principal、目录适配、ReBAC、Permission Check、Grant | Run、Tool execution、Sandbox lifecycle、Agent Catalog |
| Model Gateway | Model Data Plane | provider routing/usage facts | Provider credential、route、protocol adaptation、rate/usage | Agent orchestration、Tool policy、Run state |
| Gateway | External Trust Boundary | external authn result | OIDC/JWT、header sanitation、routing | 业务 ReBAC、Run logic |
| Console / Frontend | UX | 无可信服务端事实 | 管理、观察、发起意图、展示审批 | 构造可信 Principal、授权结果、修改 execution facts |

---

## 2. Hub 模块清单

Hub 的统一定位：**定义资产控制面**。

### 2.1 Agent Catalog

拥有：

```text
Agent
AgentDraft
AgentRevision
Agent metadata
Agent publish/deprecate lifecycle
```

输出给 Runtime：

```text
published AgentRevision reference
immutable ExecutionSpec / resolution result
```

禁止：

- 保存 Run 当前状态；
- 在 Hub 内启动 Agent Loop；
- 为每次消息动态修改已发布 Revision。

### 2.2 Skill Catalog

拥有：

```text
Skill
SkillVersion
SKILL.md / references / scripts / assets metadata
publish lifecycle
visibility/share
```

存储：控制面 metadata -> PG；大对象 -> S3/Object Storage。

Runtime 消费：精确 SkillVersion + digest。

Sandbox 只接收 Runtime 按需物化的 scripts/templates/assets，不解析 Skill instruction 决定模型上下文。

### 2.3 Tool Control Plane

目标对象：

```text
ToolProvider
Tool
ToolVersion
ToolConnection metadata
ToolDeployment
ToolPolicy
AgentToolBinding
```

Hub 负责：

- provider/import/discovery metadata；
- Tool manifest/schema；
- version/publish/deprecate；
- connection 引用，不保存明文长期 credential；
- Agent binding；
- policy definition；
- published revision 中精确 pin 版本/digest/revision。

Hub 不负责：

- 实际 Tool invoke；
- Tool retry/timeout runtime state；
- per-call approval 状态；
- credential 注入数据面。

### 2.4 ModelProfile Catalog

拥有：

```text
ModelProfile
model capabilities metadata
routing profile reference
agent binding
```

真正 provider credential、协议代理、usage/rate enforcement 应逐步进入 Model Gateway。

### 2.5 SandboxProfile Catalog

Hub 定义产品层 Profile：

```text
profile id/version
cpu/memory/gpu/storage requirements
network class
executor capabilities
workspace policy
```

Sandbox 负责把它编译为实际 K8s/agent-sandbox 基础设施。

### 2.6 Sharing / Publishing Governance

Hub 只管理自身业务资源的 visibility/share semantics；主体目录和 group membership 不在 Hub 展开，由 IAM 负责。

---

## 3. Runtime / AgentKit 模块清单

Runtime 的统一定位：**一次执行真正发生的地方**。

### 3.1 Run Engine

拥有：

```text
Run
ExecutionSnapshot
RunAttempt
RuntimeEvent
```

必须保证：

- Snapshot 模型调用前落库；
- Snapshot immutable；
- Retry 新建 Attempt；
- Event append-only；
- terminal state 原子提交；
- PostgreSQL 是执行事实源。

已删除：

```text
run_steps
pgx platform_runs/platform_run_events second store
public run-state mutation API
```

### 3.2 Execution Resolver Adapter

职责：调用 Hub 获取**已发布且可执行**的精确 ExecutionSpec。

Runtime 不允许：

- 自己选择 `latest` Tool/Skill；
- 修改 Hub Catalog；
- 把 Hub API 返回结果当作永久缓存 Catalog。

### 3.3 Context Builder

每轮模型上下文按需组装：

```text
System Core
+ Agent Instruction
+ relevant memory
+ recent conversation
+ active skill instructions
+ retrieved references
+ model-visible tool schemas
+ current user message
```

负责 token/context budget、引用裁剪、Skill/Memory/RAG 注入策略。

禁止完整 Skill 包、完整历史、完整项目文件无条件进 Prompt。

### 3.4 ADK-Go Agent Loop

生产唯一 Agent Loop。

输入：Compiled ExecutionSnapshot + ModelContext。  
输出：model/tool/events/state deltas。

Sandbox Worker 不再拥有第二 Loop。

### 3.5 Tool Compiler

目标流水线：

```text
ToolSnapshot
 -> Validator
 -> Manifest Parser
 -> Compiler
 -> AdapterFactory
 -> ExecutableTool
```

首批 adapter：

```text
sandbox
mcp
http
internal
```

Fail closed：缺失 adapter/version/schema 不得默认 internal。

### 3.6 Unified Invocation Pipeline

所有 Tool 必须经过：

```text
snapshot allowlist
-> input schema
-> normalization
-> argument/resource policy
-> IAM authorization
-> risk
-> approval
-> rate/concurrency
-> credential broker
-> idempotency
-> adapter invoke
-> output schema/size
-> redaction
-> RuntimeEvent / ToolInvocation
```

### 3.7 Approval Engine

Runtime 拥有一次执行中的 ApprovalRequest/Grant 状态。

目标模式：

```text
auto
per_run
per_call
policy_based
disabled
```

ApprovalGrant 绑定：

```text
runId
snapshotId
toolId/version
principal
inputDigest
risk
expiry
```

参数变化 -> inputDigest 变化 -> 原批准失效。

### 3.8 Credential Broker

Runtime 只请求短期 credential/opaque handle。

支持演进：

```text
OAuth user delegation
client credentials
API-key broker
mTLS
K8s workload identity
cloud STS
```

禁止长期 Secret 进入 Prompt/Snapshot/Sandbox env。

### 3.9 Model Runtime

Runtime 根据 Snapshot 中 pin 的 ModelProfile 调 Model Gateway。

Runtime 负责 agent-level model semantics；Gateway 负责 provider-level routing/protocol/credential/usage。

### 3.10 Session / Conversation

Session 是对话容器；Run 是一次执行；两者不得混为一个状态机。

Session 可绑定 Workspace/SandboxLease，但 Agent Loop 仍在 Runtime。

### 3.11 Runtime Observability

拥有/输出：

```text
RuntimeEvent Ledger
traceId
ToolInvocation
run/attempt latency
model/tool error taxonomy
SSE replay
```

Trace 是观测投影，Event Ledger 是运行事实。

---

## 4. Sandbox 模块清单

Sandbox 的统一定位：**隔离资源 + Executor**。

### 4.1 Profile Infra Adapter

输入：Hub pin 的 SandboxProfile。  
输出：实际 SandboxTemplate/资源/网络配置。

### 4.2 Lease Manager

拥有：

```text
SandboxLease
expiry
profileDigest
workspaceRef
capabilities
infra phase
```

Lease 绑定 tenant/project/session/run/snapshot/profile。

### 4.3 Workspace

负责：

- PVC/volume；
- project/session workspace；
- mount/read-only asset materialization；
- Pod 回收后 workspace 保留策略。

### 4.4 Executor / Tool Server

执行：

```text
workspace
shell
python
browser
artifact
optional local MCP stdio
```

只接收 Runtime 已验证的结构化 Tool Call。

### 4.5 Resource / Network Isolation

负责 CPU/Memory/GPU/storage、egress、process、filesystem 等硬边界。

### 4.6 Quota / Scheduler / Idle GC

基础设施资源治理，不等于业务授权。

### 4.7 明确删除方向

```text
Session Worker Agent Loop
Sandbox Model Client
Prompt Builder
Skill Router
worker :8088 Agent message endpoint
long-lived user credential
```

---

## 5. IAM 模块清单

IAM 的统一定位：**可信 Principal + 业务资源授权权威**。

### 5.1 Directory Adapter

Casdoor：

```text
Organization
User
Group hierarchy
membership
```

IAM 不再维护第二套 Organization。

### 5.2 Principal

Gateway 完成 OIDC/JWT 验证和 header sanitation；Kernel/IAM 恢复可信 Principal。

业务请求体和普通外部 Header 不能覆盖 Principal。

### 5.3 Authorization / ReBAC

SpiceDB 是 authorization projection/query engine，不是业务 metadata 主库。

支持：

```text
CheckPermission
resource relationships
subject lookup
resource lookup
```

默认 fail-closed。

### 5.4 Project / Capability / Resource / Grant Control Plane

IAM 管跨业务通用授权根与 Grant；Hub 只管理 Hub 业务对象本身。

### 5.5 Runtime 调 IAM 的典型权限

```text
tool.execute
connection.use
sandbox.create
sandbox.use
repository.issue.create
secret.delegate
```

IAM 判断资格，Runtime Approval 判断本次副作用确认，两者必须同时满足。

---

## 6. Model Gateway 模块清单

目标定位：**模型 Provider 数据面**。

负责：

```text
provider endpoints
provider credentials
OpenAI-compatible / vendor protocol adapters
routing / fallback
rate limit / quota
usage accounting
health/circuit breaker
stream normalization
```

不负责：

```text
Agent Loop
Prompt business composition
Tool selection
Run state
Skill/Memory injection
```

Runtime 始终是模型请求的业务调用方。

---

## 7. 组件依赖方向

允许的主要依赖：

```text
Console -> Gateway
Gateway -> Hub / Runtime / IAM
Hub -> IAM
Runtime -> Hub
Runtime -> IAM
Runtime -> Model Gateway
Runtime -> Sandbox
Sandbox -> agent-sandbox / K8s
Model Gateway -> model providers
IAM -> Casdoor / SpiceDB / PG
```

应禁止的反向依赖：

```text
Hub -> Runtime execution internals
Hub -> Sandbox tool data plane
Sandbox -> Hub resolve
Sandbox -> Model Gateway for Agent Loop
Sandbox -> IAM catalog logic
Model Gateway -> Hub catalog mutation
IAM -> Runtime run state
Frontend -> trusted execution-state mutation
```

---

## 8. 跨组件契约

### Hub -> Runtime: ExecutionSpec

必须 immutable/pinned：

```text
agentRevision
modelRevision/profile
skill versions + digests
tool versions + manifest digests
policy revisions
sandbox profile revision/digest
connection refs (no secret)
```

### Runtime -> Sandbox: SandboxLease request

```text
tenant/project/session/run/snapshot
profile ref + digest
workspace policy
required executor capabilities
TTL / reuse policy
```

### Runtime -> Sandbox: ToolInvocation

```text
runId
snapshotId
attemptId
toolInvocationId
canonicalToolRef
validated arguments
lease/workload identity
```

### Runtime -> IAM: Authorization Check

```text
principal
resource
action
context refs
```

返回 Allow/Deny + decision metadata；不返回业务执行结果。

### Runtime -> Model Gateway

```text
model profile/ref
normalized messages
stream options
request/trace identity
```

Gateway 不接 AgentDefinition。

---

## 9. 当前架构债务台账

| 优先级 | 债务 | Owner | 处理方向 |
| --- | --- | --- | --- |
| P0 | AgentKit `runtime.go` 仍有已不可达 generic text-run/Redis resumable 死代码 | Runtime | 物理删除，保留 `/run_live` 独立评估 |
| P0 | Sandbox 仓库 default branch 仍指向旧开发分支 | Repo governance | 切回 `main`；禁止旧 README 成为默认入口 |
| P0 | Sandbox Session Worker 镜像/entrypoint 遗留 | Sandbox | 迁移 executor 后物理删除 |
| P0 | Tool domain 仍需 Provider/Tool/Version/Connection/Policy 强类型化 | Hub | Tool Control Plane V1 |
| P0 | Runtime Tool Registry 仍需 Compiler/AdapterFactory fail-closed | Runtime | Tool Compiler V1 |
| P1 | 客户端 approval booleans | Runtime | server-side ApprovalGrant |
| P1 | Tool credential path | Runtime/IAM/Vault | Credential Broker |
| P1 | Sandbox Lease schema 缺 run/snapshot/profile digest 强绑定 | Sandbox | Lease V1 |
| P1 | Model provider 管理与 Runtime 仍需解耦 | Model Gateway | Gateway contract |
| P2 | MCP schema drift/discovery 生命周期 | Hub/Runtime | discovery revision + pinned digest |
| P2 | Context/Memory 注入标准 | Runtime | Context Builder pipeline |

---

## 10. 新功能归属判断

开发前按顺序问：

1. 这是**定义/版本/发布**吗？-> Hub。
2. 这是**一次执行事实或 Agent Loop**吗？-> Runtime。
3. 这是**隔离资源/文件/进程/浏览器**吗？-> Sandbox。
4. 这是**谁能做什么**吗？-> IAM。
5. 这是**模型 Provider 路由/凭据/协议**吗？-> Model Gateway。
6. 只是 UI 展示/用户输入吗？-> Console。

如果一个功能同时回答两个以上问题，优先拆成稳定契约，而不是把两个 Owner 合并到同一个模块。
