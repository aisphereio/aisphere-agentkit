# AgentKit 原生沙箱运行模式

本目录新增 `internal/sandboxclient`、`internal/sessionworkerclient`、`internal/sessionnative` 三个包，用于把 AgentKit session/run 接到 `aisphere-sandbox`。

目标链路：

```text
CreateSession -> Hub Resolve Agent Snapshot -> Sandbox Adapter Ensure -> agent-sandbox CRD -> Sandbox Pod Ready -> agentkit-session-worker /workspace
```

当前代码以 SDK/Client 形式提供，下一步需要在 AgentKit 的 session 创建 API 与 run 消息转发 API 中调用这些包。
