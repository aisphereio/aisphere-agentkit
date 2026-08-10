# ADR-005: Tool Connector Taxonomy V1

- Status: Accepted
- Date: 2026-08-07
- Scope: Hub / Runtime / Sandbox / IAM / Model Gateway
- Supersedes: ADR-003 §2 中“V1 只保留四种 Connector”的枚举；ADR-003 其余 Tool Contract / Invocation Pipeline 设计继续有效

## Decision

AISphere 不再用一个 `tool type` 同时表达“Tool 是什么、从哪里来、在哪里执行”。

Tool V1 使用三个互相独立的维度：

```text
Tool semantics
  = 这个 Tool 对 Agent/用户意味着什么

Tool source / discovery
  = 这个 ToolVersion 如何被发现、导入或发布

Tool connector
  = 一次 ToolInvocation 最终由哪个执行后端完成
```

只有 **connector** 决定 Runtime Adapter 路由。

业务领域名称、导入协议、代码包装格式、传输协议都不能继续冒充 connector kind。

---

## 1. Tool semantics 不是执行类型

以下都只是领域语义示例：

```text
skill.publish
skill.pull
git.push
workspace.read
knowledge.query
memory.search
browser.open
cloud.vm.restart
kubernetes.pod.logs
```

这些名称不决定执行位置。

同一个语义 Tool 在不同实现中可以使用不同 Connector。例如：

```text
knowledge.query
  -> service   # AISphere Retrieval Service
  -> mcp       # 外部知识 MCP
  -> http      # 第三方检索 API

browser.open
  -> sandbox   # AISphere browser executor
  -> model-native capability  # 某模型供应商原生 computer/browser 能力；此时不作为普通 ToolConnector
```

因此 Hub catalog 不应出现 `git/python/browser/database/skill` 这类顶层 runtime type。

---

## 2. Source / discovery 与执行分离

ToolVersion 可以来自：

```text
runtime builtin manifest
AISphere service manifest
sandbox capability/package manifest
MCP tools/list discovery
OpenAPI import
manual/admin definition
future marketplace/package import
```

Source 只影响“如何生成候选 ToolVersion”，不决定 Runtime 如何执行。

例如：

```text
OpenAPI document
  -> importer
  -> HTTP ToolVersion
  -> connector=http
```

```text
MCP tools/list
  -> discovery
  -> immutable ToolVersion
  -> connector=mcp
```

```text
Runtime Builtin manifest
  -> Hub reconciliation
  -> system ToolVersion
  -> connector=builtin
```

---

## 3. Canonical V1 Connector kinds

V1 Runtime 只承认以下五种普通 Tool 执行 Connector：

```text
builtin
service
sandbox
mcp
http
```

Runtime 必须 fail closed：未知 connector 不允许从名称、schema、endpoint、runner 或 placement 猜测执行方式。

### 3.1 builtin

执行者：trusted AISphere Runtime process。

适用：

- 编译进 Runtime binary 的可信 Go Tool；
- 不需要跨服务调用即可完成的 Runtime-owned model-callable capability。

```text
BuiltinConnector
├── builtinId
├── implementationVersion
└── descriptorDigest
```

不适用：

- Shell/Python/browser/filesystem 动态执行；
- Hub/Git 等其他服务拥有的业务写操作；
- 用户上传代码。

### 3.2 service

执行者：AISphere 内部由其他组件拥有的 trusted service。

这是 V1 新增的独立 Connector，用于避免把内部业务操作伪装成 Builtin 或通用 HTTP。

典型用途：

```text
skill.publish       -> Hub/Git service
git.repository.get  -> Hub/Git service
file.metadata.get   -> File service
knowledge.query     -> future Retrieval service
memory.query        -> future Memory service
```

推荐 contract：

```text
ServiceConnector
├── service          # hub / retrieval / file / ... logical service id
├── operation        # stable logical operation
├── contractVersion
└── targetResolver?  # 与 ToolPolicy authorization 协作
```

约束：

1. ServiceConnector 不保存任意 URL。
2. Runtime 通过本地受信任 Service Adapter/client 解析 logical service。
3. 用户 principal / delegated identity 仍需传播。
4. 目标资源 IAM 必须在 Invocation Pipeline / owning service 中执行。
5. Service 不因为是“内部服务”就绕过 Approval/Credential/Audit。

`internal` 这个历史字符串在迁移期间仍表示旧的 in-process Go/function Builtin，**不得重解释成 service**。

### 3.3 sandbox

执行者：Sandbox Tool Server / executor。

适用：

- workspace filesystem；
- shell/process；
- Python；
- browser/computer automation；
- Git working-tree 操作；
- 用户/Skill 动态代码；
- 需要强资源隔离的执行。

```text
SandboxConnector
├── capability
├── requiredCapabilities[]
└── packageRef?       # future sandbox package contract
```

模型 Tool 名与 executor capability 必须分离：

```text
model Tool: skill.pull
connector: sandbox
executor capability: git.fetch
```

Sandbox 不理解 Hub ToolVersion、业务 IAM、审批或 Agent binding，只校验 lease/workload identity/profile/capability/resource boundary。

### 3.4 mcp

执行者：远端 MCP server。

```text
MCPConnector
├── connectionRef
├── remoteToolName
├── protocolVersion
└── discoveredSchemaDigest
```

MCP 是连接/调用协议，不是 AISphere Tool 领域模型。

运行时不能动态把整台 MCP server 的所有 Tool 直接暴露给模型；Hub discovery/import 后必须形成 pinned ToolVersion。

### 3.5 http

执行者：通用 HTTP endpoint。

```text
HTTPConnector
├── connectionRef
├── method
├── pathTemplate
├── requestMapping
└── responseMapping
```

主要用于外部 SaaS/API 或没有 AISphere typed Service Adapter 的 HTTP 服务。

OpenAPI 只是 HTTP Tool 的 importer，不是 Connector。

---

## 4. 为什么 service 不能直接用 http

`service` 和 `http` 的区别是信任边界和契约 Owner，而不是网络协议。

```text
service
  logical first-party operation
  endpoint/service discovery belongs to platform deployment
  typed internal client/contract
  principal propagation is platform-defined

http
  generic connection
  endpoint belongs to ToolConnection
  request/response mapping belongs to ToolVersion/Connector
  credential injection is connection-driven
```

即使 Hub 最终通过 HTTP/gRPC 被调用，`skill.publish` 仍应是 `service` Connector，因为 Runtime 依赖的是 Hub 的逻辑服务 contract，而不是一段用户可配置 URL。

---

## 5. Model-visible capability 不一定是 Tool

以下能力明确不进入普通 ToolConnector taxonomy。

### Model-native provider capability

例如：

```text
Gemini google_search
Gemini url_context
provider-native grounding/computer-use
```

归属：`ModelProfile / provider capability`。

原因：真正执行发生在 Model Provider 内部，Runtime 无法以普通 ToolInvocation Adapter 的方式控制每一步执行。

若平台希望获得完整 ToolInvocation/IAM/Approval 控制，则应提供 AISphere 自己的 HTTP/MCP/Sandbox/Service Tool，而不是依赖 provider-native capability。

### Sub-agent / handoff / task delegation

归属：AgentRevision / Runtime orchestration protocol。

它们可以被模型选择，但目标对象是另一个 Agent/Task，不应复制成 ToolVersion。未来若接入外部 A2A Agent，需要单独 ADR 决定是否新增 `agent` Connector；V1 不预留一个含义不清的 generic type。

### Context processors

例如：

```text
preload_memory
conversation summary injection
retrieval prefetch
system prompt assembly
```

归属：Context Builder / ContextPolicy。

模型没有显式选择权时，它就不是 Tool。

### Runtime primitives

例如：

```text
Run/Event persistence
ApprovalGrant persistence
Credential Broker
trace/metrics
exit-loop protocol internals
```

归属：Runtime protocol/service，不进入 Hub Tool catalog。

---

## 6. 不是 Connector 的常见概念

```text
openapi      -> importer
stdio        -> MCP transport
sse          -> transport
websocket    -> transport
function     -> legacy alias; builtin 或 sandbox package
python       -> sandbox capability/runtime
shell        -> sandbox capability/runtime
container    -> sandbox package/executor implementation
binary       -> sandbox package implementation
wasm         -> future sandbox/runtime extension
skill        -> domain/package asset
browser      -> domain capability；通常 sandbox 或 model-native
sql/database -> domain capability；通常 service/mcp/http/sandbox
hub          -> service id，不是 placement/runtime type
```

---

## 7. ToolCompiler target model

当前 `toolruntime.Registry` 仍是迁移期字符串 resolver。

目标：

```text
ExecutionSpec ToolBinding
  -> ToolCompiler
      -> validate ToolRef / model schema / policy
      -> validate typed Connector
      -> resolve Adapter
  -> ExecutableTool
  -> BrokerBackedTool
  -> Unified Invocation Pipeline
```

AdapterRegistry：

```text
builtin -> BuiltinAdapter
service -> ServiceAdapter
sandbox -> SandboxAdapter
mcp     -> MCPAdapter
http    -> HTTPAdapter
```

所有 Adapter 必须在 Broker 后面；任何 Connector 不得拥有独立的审批、IAM、credential、audit 快捷路径。

---

## 8. Migration rules

1. Runtime connector registry 增加 canonical `service` kind。**Completed in Tool V1 PR.**
2. 历史 `internal` 继续仅作为 Builtin migration alias，不映射到 `service`。**Completed.**
3. `workspace.* / browser.* / shell/python` 迁移到 `sandbox`。
4. `skill.* / git.*` 按动作拆分：工作树执行走 `sandbox`；Hub/Git 资产操作走 `service`。
5. MCP direct Toolset 路径迁到 Broker-backed `mcp` Adapter。
6. OpenAPI 不新增 runtime type，只生成 `http` ToolVersion。
7. Runtime Builtin manifest 与 AISphere Service manifest 分开 reconciliation。
8. Model-native capabilities 移出 Hub Tool catalog，归 ModelProfile/provider capability。
9. map-based `runtime.type/execution.runner` 全量迁移到 typed `connector` 后删除 compatibility guessing。

## Invariant

**一个新能力只有在模型可以显式选择、并且 Runtime 能为这次选择建立 ToolInvocation 时，才应该成为普通 AISphere Tool。**

否则它应属于 ModelProfile、ContextPolicy、Agent orchestration、Runtime primitive 或 Sandbox implementation，而不是为了 UI 统一而强行塞进 Tool catalog。
