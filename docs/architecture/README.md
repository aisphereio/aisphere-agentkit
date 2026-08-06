# AISphere Runtime Architecture Decisions

本目录是 `aisphere-agentkit` 在 AISphere 平台中的权威架构入口。

## 当前有效决策

- [ADR-001: AISphere Runtime 所有权与唯一 Agent Loop](ADR-001-runtime-ownership.md)

## 解释优先级

当旧的 backend platform、session-native worker、environment management 或兼容链路文档与本目录中的 Accepted ADR 冲突时，以 Accepted ADR 为准。

当前方向：

```text
AgentKit = ADK Core + AISphere Runtime
```

Runtime 是唯一 Agent Loop、Context Builder、Run Engine、Tool Broker 和 Event Ledger 所在位置，但不建设第二套 Hub、IAM、Skill Registry、Model Registry 或 Sandbox 控制面。

## 历史文档处理

以下文档仍可作为现状与迁移输入，但不再代表目标边界：

- `docs/backend-platform-design.md`
- `docs/backend-platform-implementation-plan.md`
- `docs/SESSION_NATIVE_*`（若存在）
- 任何要求 Sandbox Worker 运行第二套 Agent Loop 的设计

后续代码迁移必须以 ADR-001 的唯一 Agent Loop 决策为前提。
