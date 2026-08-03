# AgentKit 原生沙箱 Session 接入说明

本版本把 AgentKit 的 session/run 主链路接入到 `aisphere-sandbox` Adapter：

```text
CreateSession
  -> Hub resolve Agent snapshot
  -> Sandbox Adapter ensure sandbox
  -> 等待 sandbox 内 agentkit-session-worker /readyz
  -> 在 ADK session state 中记录 __agent_native_sandbox__

Run / RunSSE
  -> 校验 session 存在
  -> ensure sandbox worker，支持进程重启后恢复
  -> 转发用户消息到 worker /v1/session/messages
  -> 从 worker /v1/events 长轮询
  -> 转换成 ADK Event / SSE 回传前端
```

## 开启配置

```yaml
skills:
  aihub:
    enabled: true
    agent_mode: true
    endpoint: http://aisphere-hub:8848
    runtime_id: service:agentkit-runtime-dev
    token_env: AIHUB_RUNTIME_TOKEN
    sandbox:
      enabled: true
      mode: agent-native
      native_session: true
      adapter_endpoint: http://aisphere-sandbox:18082
      adapter_token_env: AISPHERE_SANDBOX_TOKEN
      default_profile: default-python-offline
      ready_timeout_seconds: 90
      event_idle_seconds: 2
```

`mode: agent-native` 或 `native_session: true` 任一生效。未开启时，AgentKit 仍走原有本地 runner，不影响本地开发。

## API 行为

### 创建 Session

现有接口不变：

```http
POST /api/apps/{app_name}/users/{user_id}/sessions
POST /api/apps/{app_name}/users/{user_id}/sessions/{session_id}
```

原生沙箱模式下：如果没有传 `session_id`，AgentKit 会生成 `sess_<uuid>`，因为 sandbox lease 必须绑定明确 session。

### 运行 / SSE

现有接口不变：

```http
POST /api/run
POST /api/run_sse
```

原生沙箱模式下不会再调用进程内 `runner.Run`，而是转发到 sandbox worker。

## Worker HTTP Contract

Sandbox Pod 内 `agentkit-session-worker` 需要实现：

```http
GET  /readyz
POST /v1/session/messages
GET  /v1/events?runId=<runId>
```

第一版 worker 支持 JSON 长轮询：

```json
{
  "items": [
    {"type":"assistant_message","runId":"...","content":"..."}
  ]
}
```

## 当前限制

- 当前 worker 还是 stub，负责证明“session 出生在沙箱内”；完整 Agent loop 需要继续迁移到 worker。
- 当前 Run/SSE 只支持文本消息。
- Tool 执行应在 worker 内通过 `127.0.0.1:18081` 调本地 tool-server。
