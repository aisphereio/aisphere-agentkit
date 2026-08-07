# AISphere Tool Inventory V1

- Status: Active migration ledger
- Date: 2026-08-07
- Implementation checkpoint: complete connector taxonomy + typed `ConnectorSpec` + first `ToolCompiler` boundary are in code; Builtin V1 batch 1 registered; conflicting PlanRun/local retrieval entries retired
- Verification target: Runtime Build/Test must validate the compiler migration boundary before adapters stop reading legacy Hub maps
- Change gate: do not remove adapter compatibility reads until this compiler checkpoint is verified
- Scope: `internal/configurable`, `tool/*`, Runtime Tool V1

本文不是新的 Tool 领域模型；完整 Tool Connector 决策以 ADR-003/ADR-004/ADR-005 为准。本文只回答当前 AgentKit 已注册能力 **现在是什么、目标应该放在哪里、何时删除旧入口**。

## Classification rule

```text
Runtime Builtin
  = trusted model-callable implementation compiled into Runtime

AISphere Service Tool
  = model-callable first-party platform operation executed by another trusted service

Sandbox Capability
  = filesystem/process/python/browser/Git/untrusted execution primitive

MCP Tool
  = Hub-pinned remote MCP tool executed through MCPAdapter

HTTP Tool
  = Hub-pinned external HTTP operation executed through managed ToolConnection

Runtime Primitive
  = Runtime/ADK protocol behavior; not a Hub Tool catalog asset

Model-native Capability
  = provider/model request feature executed by model provider

Domain/Product Tool
  = application-specific capability; must not live in generic Runtime core

Retire
  = duplicate/temporary architecture that conflicts with current AISphere ownership
```

Connector taxonomy is independent from the business/domain classification above. Ordinary Tool execution has exactly five canonical Connector kinds:

```text
builtin | service | sandbox | mcp | http
```

`skill/git/browser/python/database/openapi/stdio/container` are not Connector kinds.

## Current global configurable registry

| Current registration | Current shape | V1 target | Action |
|---|---|---|---|
| `save_artifact` | Tool | Runtime Builtin | **MIGRATED V1** |
| `load_artifacts` | Tool | Runtime Builtin | **MIGRATED V1** |
| `list_artifacts` | Tool | Runtime Builtin | **MIGRATED V1** |
| `delete_artifact` | Tool | Runtime Builtin | **MIGRATED V1** |
| `load_memory` | Tool | Runtime Builtin | **MIGRATED V1**; backend remains `memory.Service` port |
| `get_user_choice` | Tool | Runtime Builtin | **MIGRATED V1**; interaction, not authorization |
| `request_user_form` | Tool | Runtime Builtin | **MIGRATED V1**; interaction, not authorization |
| `exit_loop` | Tool | Runtime Primitive | Keep for ADK LoopAgent compatibility; never mirror to Hub Tool catalog |
| `preload_memory` | Tool-shaped request processor | Context/Runtime Primitive | Do not mirror; move under Context Builder policy when Context V1 lands |
| `AgentTool` | Tool factory | Agent orchestration primitive | Do not mirror; Agent/Task/Handoff orchestration remains Runtime protocol |
| `LongRunningFunctionTool` | wrapper/meta factory | Runtime Primitive | Do not mirror; remove from production Tool catalog semantics |
| `ExampleTool` | request decorator | Dev/Runtime Primitive | Do not mirror; keep only for SDK/local configuration if still needed |
| `google_search` | Gemini native tool | Model-native capability | Move under ModelProfile/provider capability, not Runtime Builtin |
| `url_context` | Gemini native tool | Model-native capability | Move under ModelProfile/provider capability |
| `google_maps_grounding` | Gemini native tool | Model-native capability | Move under ModelProfile/provider capability |
| `McpToolset` | Toolset | MCP Connector | Transitional direct ADK path; replace with Broker-backed MCPAdapter |
| `McpHttpToolset` | Toolset | MCP Connector | Transitional direct ADK path; replace with Broker-backed MCPAdapter |
| `SessionWorkspaceToolset` | local Runtime filesystem Toolset | Sandbox Capability | Remove Runtime-local filesystem execution; use Sandbox workspace executor |
| `EnvToolset` | environment/shell Toolset | Sandbox Adapter | Must not execute free-form shell in trusted Runtime; remove current direct path |
| `SkillAuthoringToolset` | Runtime-local filesystem Skill CRUD | Hub service / Sandbox Git split | Hub owns Skill definitions; worktree/Git execution belongs in Sandbox |
| `files_retrieval` | local keyword retrieval | Retire -> Retrieval port/OceanBase | **RETIRED FROM GLOBAL REGISTRY**; replace with Retrieval port + OceanBase adapter |
| `FilesRetrieval` | alias | Retire | **RETIRED FROM GLOBAL REGISTRY** |
| `PlanRunToolset` | artifact-backed second Run/loop state | Retire | **RETIRED FROM GLOBAL REGISTRY**; canonical Runtime facts replace it |
| `ProjectArtifactToolset` | project/domain artifact Toolset | Domain/Product + future storage port | Remove from generic Runtime registry; redesign after File/Artifact/OceanBase contract |
| `UploadToolset` | platform upload Toolset | Domain/Product + File service | Remove from generic Runtime registry; Runtime should consume file refs/ports |
| `NovelStoreToolset` | novel-specific PG/ObjectStore tools | Domain/Product | Extract from AgentKit core; do not migrate into Tool V1 Builtin |
| `BookPreprocessorToolset` | book-specific preprocessing | Domain/Product / Sandbox package | Extract from generic Runtime core |
| `BookSkillRunToolset` | book-specific workflow | Domain/Product | Extract from generic Runtime core |

## Automatically assembled capabilities that are not Hub Tools

The following model-visible capabilities may still be assembled by Runtime, but they are protocol/domain primitives rather than selectable Hub Tool assets:

```text
SkillToolset load/list resource helpers
Sub-agent task runner
ADK transfer/handoff helpers
Tool confirmation protocol
Context preload processors
```

Their existence is derived from another explicit AgentRevision feature (for example `Skills` or `SubAgents`) and must not be duplicated as ToolVersion bindings.

## First V1 Runtime Builtin batch

```text
artifact
├── save_artifact@1
├── load_artifacts@1
├── list_artifacts@1
└── delete_artifact@1

memory
└── load_memory@1

interaction
├── get_user_choice@1
└── request_user_form@1
```

These are registered in `builtinruntime.DefaultRegistry()` and are **not automatically exposed**. Agent exposure still requires an explicit Tool binding in AgentRevision/ExecutionSnapshot.

Their descriptor schema is derived from the code-owned ADK `FunctionDeclaration`; Hub must not maintain a second hand-written schema source for the same implementation version.

## ToolCompiler checkpoint

Runtime now has a typed connector domain and a single migration compiler boundary:

```text
CompileV1(binding, ConnectorSpec)
  -> strict typed validation
  -> never reads legacy runtime/execution maps to choose an adapter

CompileLegacy(binding)
  -> only pre-V1 snapshots
  -> Legacy=true
  -> centralized, temporary compatibility
```

`CompileLegacy` deliberately allows only trust-boundary-safe mappings:

- old Builtin can temporarily omit `implementationVersion`;
- old Sandbox can temporarily fall back from an absent explicit executor capability to the Tool id;
- stale Hub `runtime.type=builtin + placement=sandbox` is corrected to Sandbox;
- Service must provide explicit logical `service + operation`; URL/name inference is rejected;
- HTTP must provide `connectionRef + method + pathTemplate`; raw URL/OpenAPI definitions are rejected for Hub migration;
- MCP may use the pre-V1 logical `server` plus pinned remote Tool name.

No adapter should add new compatibility parsing. Once Hub emits typed Tool V1 ExecutionSpec, new Runs must use `CompileV1`; `CompileLegacy` is then deleted after old snapshot/data migration.

## Immediate cleanup sequence

### P0

1. `PlanRunToolset` removed from global configurable resolution. Canonical Runtime Run facts are the only run lifecycle.
2. `files_retrieval` / `FilesRetrieval` removed from global configurable resolution. Retrieval waits for Retrieval port + OceanBase adapter.
3. Tool semantics/source/connector taxonomy fixed to `builtin/service/sandbox/mcp/http`.
4. Typed `ConnectorSpec` + ToolCompiler migration boundary established.
5. Sandbox capability contract V1 established cross-repo; workspace/browser current capabilities verified.
6. After compiler verification, route SandboxAdapter from compiled capability rather than raw Hub maps.
7. Stop direct Runtime-local Skill authoring; current `skill.fetch/pull/push/tag/publish` require future Sandbox `git.*` capabilities and fail closed until then.

### P1

1. Extract novel/book/project-upload toolsets from generic AgentKit Runtime core.
2. Replace direct `McpToolset` execution with Broker-backed MCPAdapter.
3. Implement ServiceAdapter and HTTPAdapter behind the same Broker.
4. Move Gemini-native capabilities to ModelProfile/provider capabilities.
5. Remove legacy configurable/map fallback after all production AgentRevision bindings use typed Tool V1 connectors.

## Invariant

Adding a new entry to `internal/configurable` does **not** make it an AISphere Builtin or a valid AISphere ToolConnector.

A production Runtime Builtin exists only when it has an explicit code-owned `BuiltinDescriptor + Factory` registration in `builtinruntime.DefaultRegistry()` and a corresponding mirrored immutable Hub system ToolVersion.

Every ordinary ToolInvocation must ultimately resolve to exactly one typed connector (`builtin/service/sandbox/mcp/http`) and pass through the unified Runtime Tool Broker.