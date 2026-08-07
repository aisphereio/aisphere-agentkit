# ADR-003: Tool Contract 与统一 Invocation Pipeline V1

- Status: Proposed
- Date: 2026-08-07
- Scope: Hub / Runtime / Sandbox / IAM

## Context

当前 AISphere 的 Tool 模型混合了多个不同层级：

- Hub `ToolDefinition` 同时承载模型可见 schema、协议类型、endpoint、credential ref、执行 placement、runner、container/python/wasm 参数、Sandbox 资源策略、IAM capability、timeout/retry；
- Hub Agent resolve 还接收 `approvalConfirmed` / `approvedTools`，把一次 Run 的审批事实混入 definition snapshot；
- Runtime `runtimeplan.ToolBinding` 使用多个 `map[string]interface{}`，并从 `runtime.type` / `execution.runner` / name 等字段猜测执行方式；
- Runtime `PermissionGate` 依赖 definition 中的 `Approved bool`，把模型 callback confirmation 与服务端授权事实混在一起；
- MCP、Sandbox、Builtin 目前可以沿不同路径执行，缺少一个统一的 ToolInvocation 事实和执行中间件；
- Sandbox 仍接收 `allowedTools/toolSchemas/runtimePlan` 等过渡 metadata，业务授权边界不够清晰。

继续在现有 `ToolDefinition` 上增加字段会让 Definition Plane、Execution Plane 与 Resource Plane 更难分离。

## Decision

AISphere Tool V1 拆成五个明确层次：

```text
Tool / ToolVersion             Hub: 模型语义与不可变版本
ToolConnector                  Hub: 如何连接实现
ToolConnection                 Hub: 环境级连接与 credential reference
ToolPolicy                     Hub: 声明式执行策略
ToolInvocation                 Runtime: 一次真实执行事实
```

运行时只有一条调用链：

```text
ADK Agent Loop
  -> BrokerBackedTool
  -> ToolCompiler / resolved ExecutableTool
  -> Unified Invocation Pipeline
       -> schema validation
       -> IAM authorization
       -> approval
       -> credential broker
       -> timeout/rate/concurrency
       -> adapter
       -> result normalization
       -> ToolInvocation + RuntimeEvent
  -> FunctionResponse
```

MCP、HTTP、Sandbox 和 Runtime Builtin 均不能绕过该链路。

---

## 1. Hub: Tool 与 ToolVersion

### Tool

`Tool` 是稳定的 catalog identity，只描述资产级信息：

```text
Tool
├── id
├── canonicalName
├── displayName
├── description
├── scope
├── status
├── labels/tags
├── latestVersion
└── IAM relationships
```

`canonicalName` 是模型/API 使用的稳定名称；`displayName` 仅用于 UI。

### ToolVersion

`ToolVersion` 是不可变发布版本：

```text
ToolVersion
├── toolRef
│   ├── toolId
│   ├── version
│   ├── revision
│   └── digest
├── model
│   ├── name
│   ├── description
│   ├── inputSchema
│   ├── outputSchema
│   └── annotations
├── connector
├── defaultPolicy
└── metadata
```

约束：

1. AgentRevision 必须 pin `ToolVersion`，Run 时禁止解析 `latest`。
2. ToolVersion 不包含 Run/Session/ApprovalGrant/credential value。
3. ToolVersion 不包含 Kubernetes Pod/container 资源编排细节。
4. input/output schema 使用 JSON Schema 2020-12 语义；服务端负责 canonicalize + digest。

---

## 2. Hub: ToolConnector 是 typed union

V1 只保留四种执行 Connector：

```text
builtin
sandbox
mcp
http
```

禁止继续使用一个字符串 `runtime.type` + 一个字符串 `execution.runner` 组合猜执行路径。

推荐 contract：

```proto
message ToolConnector {
  oneof kind {
    BuiltinConnector builtin = 1;
    SandboxConnector sandbox = 2;
    MCPConnector mcp = 3;
    HTTPConnector http = 4;
  }
}
```

### BuiltinConnector

Runtime 内可信 Go implementation：

```text
BuiltinConnector
└── builtinId
```

例如平台内部只读/控制能力，但仍必须经过 Invocation Pipeline。

### SandboxConnector

Hub Tool 映射到 Sandbox 提供的 executor capability：

```text
SandboxConnector
├── capability          # workspace.read / shell.exec / python.exec / browser.open ...
└── requiredCapabilities[]
```

Sandbox 只理解 capability，不理解 Agent Tool catalog、ToolVersion、业务 IAM 或审批。

### MCPConnector

```text
MCPConnector
├── connectionRef
├── remoteToolName
├── discoveredSchemaDigest
└── protocolVersion
```

MCP 是 Connector，不是 AISphere Tool 的领域模型。

MCP discovery 负责把 remote `tools/list` 转成 Hub Tool candidate；发布后 ToolVersion pin schema digest。运行时可按 drift policy 校验远端 schema 是否仍兼容。

### HTTPConnector

```text
HTTPConnector
├── connectionRef
├── method
├── pathTemplate
├── requestMapping
└── responseMapping
```

OpenAPI 是 **import/discovery source**，不是 runtime type。OpenAPI operation 导入后生成一个或多个 HTTP ToolVersion。

### V1 不支持的顶层 runtime type

以下概念不再作为平台 Tool runtime type：

```text
openapi      -> importer
stdio        -> MCP connection transport
function     -> builtin 或未来 sandbox package
container    -> Sandbox 实现细节/未来 sandbox_package
python       -> Sandbox executor capability
binary       -> Sandbox 实现细节
wasm         -> 未来扩展，不进入 V1
hub          -> 禁止 placement，Hub 不执行 Tool
```

未来若需要用户自定义容器化 Tool，新增明确的 `sandbox_package` Connector/Package contract，不恢复通用 `runner=image+command+env` 大杂烩。

---

## 3. Hub: ToolConnection 与 Secret 分离

ToolVersion 不直接保存 endpoint secret/header secret。

```text
ToolConnection
├── connectionId
├── providerType        # mcp/http/...
├── scope               # org/project/private
├── revision
├── endpoint / transport config
├── credentialRef       # reference only
├── tls/network policy refs
└── status
```

ExecutionSpec 必须 pin connection revision/digest，避免 ToolVersion 固定但连接配置静默漂移。

Credential value 永远不能进入：

- Hub Agent/Tool snapshot；
- ExecutionSpec / ExecutionSnapshot；
- Prompt / ModelContext；
- Skill package；
- Sandbox workspace；
- Tool arguments（除非它本身就是用户业务数据）。

Runtime Credential Broker 在调用前按 principal + connectionRef 获取短生命周期 credential，并只注入具体 Adapter。

---

## 4. Hub: ToolPolicy 是声明，Runtime 是执行者

```text
ToolPolicy
├── approval
│   └── mode: none | per_run | per_call
├── authorization[]
│   ├── action
│   └── resourceResolver
├── timeout
├── retry
├── rateLimit
├── concurrency
├── risk
│   ├── readOnly
│   ├── destructive
│   ├── idempotent
│   └── openWorld
└── resultPolicy
```

`authorization` 必须能解析具体 target resource，而不能只保留无资源语义的 capability string。

示例：

```text
action: git.push
resourceResolver:
  source: input
  path: repositoryId
  format: "repository:{value}"
```

Hub IAM 负责 Tool 资产本身的 view/edit/execute；Runtime 在 ToolInvocation 时进一步检查 target-resource 权限。

### Approval 规则

ToolPolicy 中只有审批 **策略**，不存在 `Approved bool`。

真实审批事实是 Runtime `ApprovalGrant`：

```text
ApprovalGrant
├── grantId
├── runId
├── principal
├── toolRef
├── scope: run | call
├── inputDigest?        # per_call 必须绑定
├── grantedBy
├── grantedAt
└── expiresAt
```

ADK `RequestConfirmation` 可以作为用户交互机制，但不是授权事实源；真正执行前 Broker 必须读取服务端 ApprovalGrant。

---

## 5. AgentRevision 中使用 AgentToolBinding

Agent 不复制整个 Tool definition，而是绑定不可变版本：

```text
AgentToolBinding
├── toolRef
│   ├── toolId
│   └── version/revision
├── connectionRef?      # 可选择项目/环境连接
├── policyOverride?     # 只允许变得更严格
├── modelNameOverride?
└── enabled
```

Hub `Resolve AgentRevision` 生成 `ExecutionSpec` 时计算 effective policy，并输出全部 pinned tools。

**审批状态不能决定某个 Tool 是否进入 ExecutionSpec。**

---

## 6. ExecutionSpec ToolBinding

Hub -> Runtime 的 V1 contract：

```text
ExecutionSpec.tools[]
├── ref
│   ├── toolId
│   ├── version
│   ├── revision
│   └── digest
├── model
│   ├── name
│   ├── description
│   ├── inputSchema
│   ├── outputSchema
│   └── annotations
├── connector           # typed union
├── connectionRef?      # revision/digest pinned
└── effectivePolicy
```

明确删除：

```text
Approved
RequiresApproval        # 可由 effectivePolicy 推导
Runtime map[string]any
Execution map[string]any
Retry map[string]any
Hub snapshotId tied to session
approvalConfirmed
approvedTools
```

Runtime 收到 ExecutionSpec 后，将其作为自身 `ExecutionSnapshot` 的组成部分固化。

---

## 7. Runtime: ToolCompiler

`ToolCompiler` 在模型第一次调用前把 `ExecutionSpec.tools` 编译成强类型运行对象：

```text
ExecutionSpec ToolBinding
   -> validate version/digest/schema/connector
   -> resolve pinned connection descriptor
   -> compile effective policy
   -> ExecutableTool
```

```go
type ExecutableTool struct {
    Ref       ToolRef
    Model     ModelToolSchema
    Connector Connector
    Policy    EffectiveToolPolicy
}
```

ADK 侧注册的是 `BrokerBackedTool`，而不是让 MCPToolset/Sandbox/Internal 各自直接执行。

`toolruntime.Registry` 的字符串 resolver 机制迁移为：

```text
ToolCompiler
AdapterRegistry
```

AdapterRegistry 只按 typed connector kind 路由，不再从 map/name 猜测类型。

---

## 8. Runtime: Unified Invocation Pipeline

每一次模型 tool_call 都产生 `ToolInvocation`：

```text
ToolInvocation
├── invocationId
├── runId
├── attemptId
├── snapshotId
├── toolRef
├── principalRef
├── canonicalInput
├── inputDigest
├── status
├── approvalGrantId?
├── credentialLeaseRefs[]
├── connectorKind
├── startedAt
├── completedAt
├── resultSummary
├── artifactRefs[]
└── error
```

唯一执行顺序：

```text
1. Resolve exact ExecutableTool from ExecutionSnapshot
2. Validate + canonicalize input
3. Create ToolInvocation(pending)
4. Check tool asset + target-resource IAM
5. Evaluate approval policy / ApprovalGrant
6. Acquire credential lease if required
7. Apply timeout / retry / rate / concurrency policy
8. Execute Adapter
9. Normalize result / redact secrets / externalize large artifacts
10. Commit ToolInvocation terminal state + RuntimeEvent
11. Return normalized FunctionResponse to ADK
```

任何 Adapter 都不能跳过步骤 3-10。

### RuntimeEvent

建议至少：

```text
tool.invocation.created
tool.authorization.denied
tool.approval.required
tool.approval.granted
tool.execution.started
tool.execution.completed
tool.execution.failed
```

Hub 的 `ListToolFailures` 删除；失败查询来自 Runtime `ToolInvocation + RuntimeEvent`。

---

## 9. Adapter contract

V1：

```text
BuiltinAdapter
SandboxAdapter
MCPAdapter
HTTPAdapter
```

统一输入：

```text
InvocationRequest
├── invocation identity
├── executable tool ref
├── validated arguments
├── principal / trace references
├── credential lease handles
└── deadline
```

统一输出：

```text
ToolObservation
├── text?
├── structuredContent?
├── artifactRefs[]
├── metadata
├── truncated
└── error?
```

Result Normalizer 控制模型上下文大小、敏感字段、二进制/大文件外置。

---

## 10. Sandbox contract

Sandbox 不再接收 Hub Tool catalog 作为授权事实。

Sandbox 只提供 executor capabilities：

```text
workspace.read
workspace.write
workspace.list
shell.exec
python.exec
browser.open
browser.action
artifact.export
...
```

Runtime `SandboxAdapter` 将 ToolVersion 的 `SandboxConnector.capability` 映射为 executor request：

```text
SandboxExecuteRequest
├── runId
├── attemptId
├── snapshotId
├── toolInvocationId
├── sandboxLeaseId
├── capability
├── arguments
└── deadline
```

Sandbox 校验：

- Lease/tenant/workload identity；
- profile capability allowlist；
- filesystem/network/resource boundary；
- executor-local argument safety。

Sandbox **不再**判断：

- 用户是否有 Git/Skill/Cloud 业务权限；
- Tool 是否获得用户审批；
- Agent 是否允许绑定该 Tool；
- Model 是否应该看到该 Tool。

这些均在 Runtime Broker 之前完成。

---

## 11. MCP lifecycle

MCP Provider/Connection 的推荐流程：

```text
Create MCP Connection
  -> Discover tools
  -> cache discovery result
  -> choose/import remote tools
  -> create Tool + immutable ToolVersion
  -> pin remoteToolName + schemaDigest + connection revision
  -> bind to AgentRevision
```

运行时：

```text
ToolInvocation
  -> MCPAdapter
  -> optional schema-drift check
  -> tools/call
  -> normalized ToolObservation
```

不允许 Agent 在运行时直接拿整台 MCP server 的动态 Toolset 绕过 Hub catalog/version/policy。

对于非常大的 MCP catalog，后续可以增加 `ToolSetRevision` 或 searchable/lazy exposure，但仍必须生成 pinned discovery digest 和走 Broker。

---

## 12. OpenAPI lifecycle

OpenAPI 仅作为导入器：

```text
OpenAPI document
  -> parse operations
  -> select operation(s)
  -> derive JSON Schema + HTTP mapping
  -> create HTTP ToolVersion(s)
```

运行期完全不需要 OpenAPI parser；只执行已经 pin 的 HTTPConnector。

---

## 13. Model exposure

V1 默认 AgentRevision 中 enabled 的 Tool 都可被模型看到。

为后续大量 Tool 做预留：

```text
exposure: eager | lazy | searchable
```

但 exposure 只影响 Context/Tool schema 注入，不影响 Runtime Broker 的执行授权。

---

## 14. 必须删除/迁移的现有实现

### Hub

删除/替换：

- `ToolRuntimeDefinition` + `ToolExecutionDefinition` 双字符串推断模型；
- `placement=hub`；
- ToolDefinition 中 `image/command/env/secret_refs/resources` 通用 runner 配置；
- `ResolveTool(runtime_id, session_id)`；
- `ListToolFailures`；
- Agent resolve 请求中的 `approvalConfirmed/approvedTools`；
- Hub Agent resolver 生成 session-specific Tool approval snapshot；
- Hub Sandbox usecase 中业务 Tool proxy/静态 tool registry 最终退役。

保留：

- Tool catalog；
- immutable ToolVersion；
- digest；
- share/IAM asset relationships；
- scope/status/version lifecycle。

### Runtime

删除/替换：

- `runtimeplan.ToolBinding.Runtime map[string]interface{}`；
- `Execution map[string]interface{}`；
- `Approved bool`；
- `PermissionGate` 作为最终授权事实源；
- string-based runtime guessing；
- MCP direct Toolset execution bypass。

迁移到：

- `ToolCompiler`；
- `ExecutableTool`；
- `InvocationPipeline`；
- `AdapterRegistry`；
- `ApprovalGrant`；
- `ToolInvocation`。

### Sandbox

删除/替换：

- `workerEndpoint` 语义；
- Agent/model runtime metadata；
- Hub Tool catalog 作为 Sandbox 业务授权源。

保留：

- executor capability discovery；
- tools endpoint；
- Lease/Profile/Workspace；
- resource/network isolation；
- artifact/resource usage output。

---

## 15. Migration sequence

不要一次重写全部 Tool 模块，按以下顺序：

```text
T0  Contract
    - ADR Accepted
    - ToolManifestV1 / Connector / Policy / Invocation contracts

T1  Runtime seam
    - 新 ToolCompiler + AdapterRegistry
    - 旧 ToolBinding -> 新 ExecutableTool compatibility compiler
    - 所有现有 adapter 先统一穿过 Broker

T2  Invocation facts
    - ToolInvocation persistence
    - RuntimeEvent
    - server-side ApprovalGrant
    - Credential Broker seam

T3  Hub V1 definition
    - 新 Tool proto/data model
    - immutable version migration
    - Agent resolve 输出 typed ToolBinding
    - 删除 approvalConfirmed/approvedTools

T4  Connectors
    - Builtin
    - Sandbox
    - MCP
    - HTTP
    - OpenAPI importer

T5  Sandbox cleanup
    - executor capability contract
    - 删除 transitional runtimePlan/allowedTools/toolSchemas metadata

T6  删除 compatibility compiler 与旧 ToolDefinition
```

关键原则：先在 Runtime 建立唯一执行 chokepoint，再替换 Hub definition contract；否则新旧 Tool definition 会再次产生两条执行链。

---

## Invariants

1. 一个 ToolInvocation 必须且只能经过一次 Runtime Invocation Pipeline。
2. Hub 只保存定义与 policy，不保存每次执行的 approval/failure 状态。
3. Runtime 是 ToolInvocation / ApprovalGrant / RuntimeEvent 的唯一事实 Owner。
4. Sandbox 是 executor，不是 Tool 业务授权系统。
5. Credential value 不进入 Hub snapshot、ExecutionSnapshot、Prompt、Skill、Sandbox workspace。
6. AgentRevision 和 ExecutionSnapshot 必须 pin ToolVersion + ConnectionRevision。
7. MCP/HTTP/Sandbox/Builtin 的执行语义统一，区别只存在于 Adapter。
8. OpenAPI 是 importer，不是 runtime。
9. Tool annotations/risk hints 不能替代 IAM 或 Approval policy。
10. 所有 Tool result 在进入模型上下文前必须经过 Result Normalizer。

## Consequences

### Positive

- Tool catalog、执行、Sandbox 与权限边界清晰；
- 可以在不修改 Agent Loop 的情况下增加新 Connector；
- MCP/OpenAPI/HTTP 不再各自长出一套治理；
- Tool invocation 可审计、可重试、可审批、可观测；
- Sandbox 保持零信任；
- AgentRevision/ExecutionSnapshot 可复现。

### Cost

- Hub Tool proto/data model 需要破坏性迁移；
- Runtime 当前 `runtimeplan/toolruntime/permissiongate` 需要重构；
- Hub Agent resolve 的 approval 逻辑需要删除；
- 需要新增 Runtime ToolInvocation/ApprovalGrant contract；
- MCP 需要 discovery/import 与 schema drift 策略。

项目尚未上线，因此接受上述破坏性重构，不长期维持两套 Tool contract。
