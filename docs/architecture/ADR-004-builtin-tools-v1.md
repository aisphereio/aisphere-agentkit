# ADR-004: Builtin Tool V1

- Status: Accepted
- Date: 2026-08-07
- Scope: Hub / Runtime
- Verification checkpoint: first code-owned Builtin batch and first retirement batch are implemented on the Tool V1 branch

## Decision

AISphere V1 将 Builtin Tool 定义为：**由 AISphere Runtime 信任并编译进 Runtime binary 的模型可调用能力**。

Builtin 不是“所有 Agent 默认拥有的 Tool”，也不是 Hub 下发到 Runtime 的代码插件。

```text
Runtime source code
  -> BuiltinDescriptor + Factory
  -> agentkit-runtime image
  -> Runtime BuiltinRegistry

BuiltinDescriptor manifest
  -> Hub system Tool catalog mirror

AgentRevision explicit Tool binding
  -> ExecutionSpec / ExecutionSnapshot
  -> Runtime exact local lookup
  -> ADK Tool declaration
  -> model tool_call
  -> Runtime execution
```

## 1. Ownership

### Runtime owns

- Builtin executable Go implementation;
- `BuiltinDescriptor` source of truth;
- `BuiltinRegistry` and exact implementation compatibility;
- descriptor manifest generation;
- actual Builtin execution.

### Hub owns

- system Tool catalog mirror;
- immutable ToolVersion used by AgentRevision;
- Agent -> ToolVersion binding;
- Tool presentation/search/version governance.

Hub does **not** store or transfer Builtin executable code.

## 2. Selection semantics

`BuiltinRegistry` is the complete set of implementations available in a Runtime binary.

It is **not** the set exposed to every Agent.

An Agent sees only the Tool versions explicitly bound in `AgentRevision.Tools` and pinned into the Run `ExecutionSnapshot`.

```text
BuiltinRegistry = Runtime capability superset
AgentRevision.Tools = explicit selected subset
ADK Tools = compiled selected subset only
```

Runtime must never silently append hidden/default Builtin Tools to an Agent. Product defaults, if needed, must be materialized as explicit AgentRevision bindings.

## 3. V1 authorization rule

Builtin Tool **selection itself has no dedicated Tool AuthZ** in V1.

Any authenticated AISphere user who is already allowed to create/edit an Agent may select a system Builtin Tool from the catalog.

Consequences:

- no `tool#viewer/executor` relationship is required for Builtin selection;
- no Builtin-level `tool.execute` IAM check is required merely to bind or expose a system Builtin;
- no per-call Builtin asset AuthZ gate is added in V1;
- AgentRevision binding is capability assembly, not an IAM grant.

This rule does **not** bypass target-resource authorization.

If a Builtin invokes a protected resource operation, that concrete target service still enforces its own resource permission using the authenticated principal. Example:

```text
Agent selects builtin skill.publish
  -> model calls skill.publish
  -> Runtime executes trusted builtin adapter
  -> Hub/Git resource service checks publish on concrete skill/repository
  -> allow or deny
```

The Builtin catalog itself is open to authenticated users; protected business resources are not.

## 4. Runtime primitives are not Builtin Tools

Runtime-internal operations that the model must not choose are not entered into the Tool catalog:

- Run/Event persistence;
- Context Builder internals;
- ExecutionSnapshot reads;
- Credential Broker internals;
- Approval persistence;
- tracing/metrics;
- internal state transitions.

These are Runtime primitives/services, not model-callable Builtin Tools.

## 5. Descriptor contract

The code-owned descriptor contains only non-secret execution identity and model-facing schema:

```text
BuiltinDescriptor
├── id
├── implementationVersion
├── model
│   ├── name
│   ├── description
│   ├── inputSchema
│   └── outputSchema
├── annotations
└── digest
```

`implementationVersion` identifies the Runtime implementation contract. It is distinct from Hub's catalog/version label.

Descriptor digest must be deterministic and is used to detect catalog/runtime drift.

## 6. Manifest

Runtime can export a descriptor-only manifest:

```json
{
  "runtimeVersion": "...",
  "builtins": [
    {
      "id": "memory.search",
      "implementationVersion": "1",
      "model": {
        "name": "memory_search",
        "description": "Search memory",
        "inputSchema": {},
        "outputSchema": {}
      },
      "digest": "..."
    }
  ]
}
```

The manifest contains no:

- Go source;
- executable binary payload;
- credential value;
- endpoint secret;
- Sandbox lease;
- Runtime-local dependency handle.

Hub may reconcile this manifest into `system/builtin` ToolVersion records.

## 7. Exact runtime resolution

At Run preparation:

```text
ExecutionSnapshot ToolBinding
  -> connector.kind=builtin
  -> builtinId + implementationVersion + descriptorDigest
  -> Runtime BuiltinRegistry exact lookup
```

If the required implementation is unavailable, Runtime fails closed with a preparation error. Runtime must not substitute `latest` or silently upgrade the implementation.

During migration, if an old binding does not carry `implementationVersion`, Runtime may resolve only when exactly one implementation version of that builtin exists locally.

## 8. Model exposure

ADK receives only the explicitly selected compiled Tool objects. The model receives their function/tool declarations, not Runtime implementation details.

Model-visible:

- name;
- description;
- input schema;
- output schema/annotations where supported.

Model-hidden:

- Runtime package/type;
- Go implementation;
- Runtime Pod;
- credentials;
- IAM relationships;
- internal dependency handles.

## 9. Dynamic/user code is not Builtin

User-supplied Python, shell, binaries, containers, Skill scripts or other dynamic code must never be downloaded into the trusted Runtime process as a Builtin.

Such code belongs to the Sandbox/package execution path.

## 10. Migration

1. Add Runtime-owned `BuiltinRegistry` and descriptor manifest. **Completed.**
2. Keep the existing configurable factory registry only as a temporary compatibility source. **In progress; V1 pinned bindings already fail closed and cannot fall back.**
3. Move current true Runtime Builtins into explicit code registrations. **First batch completed:** `save_artifact`, `load_artifacts`, `list_artifacts`, `delete_artifact`, `load_memory`, `get_user_choice`, `request_user_form`.
4. Reclassify `workspace.*`, browser/process/Python/Shell capabilities as Sandbox tools rather than Runtime Builtins. **Next P0.**
5. Replace Hub-owned hard-coded Builtin seeds with Runtime manifest reconciliation. **Pending.**
6. Remove legacy aliases (`go`, `function`, generic `internal`) after all AgentRevision bindings use typed `builtin` connectors. **Pending.**

The current migration inventory is maintained in `TOOL-INVENTORY-V1.md`. `PlanRunToolset` and the temporary local `files_retrieval` aliases have already been retired from global configurable resolution because they conflict with the accepted Run and Retrieval architecture.