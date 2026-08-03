# runtime.log SSE 标准事件

## 目标

`runtime.log` 是 run_sse 面向前端日志窗口的标准事件协议。它用于把 Agent Runtime 的关键过程事件实时推给前端，而不是把过程日志混入聊天正文。

它不是替代 JSONL trace，也不是替代 Redis resumable run：

- JSONL trace 保存详细调试记录；
- Redis resumable run 缓存 SSE 流，支持刷新恢复；
- `runtime.log` 是 SSE 数据帧里的前端展示协议；
- 聊天框只展示用户消息、最终回答、必要的交互卡片。

## 数据格式

run_sse 会推送普通 SSE data 帧，JSON 结构如下：

```json
{
  "type": "runtime.log",
  "run_id": "...",
  "invocation_id": "...",
  "app_name": "novel_pipeline",
  "user_id": "admin",
  "session_id": "...",
  "agent_name": "chapter_writer_agent",
  "event_type": "tool.call",
  "time": "2026-06-01T06:30:00Z",
  "data": {}
}
```

前端应优先使用 `type` 分流：

```text
if event.type == "runtime.log" -> LogPanel
else -> Chat/Event Display
```

## 为什么用 JSON type 而不是只用 SSE named event

当前前端 SSE parser 已经把 `data:` 解析成 JSON，并且 Redis resumable run 也把数据帧持久化在 stream 中。为了兼容已有刷新恢复逻辑，第一版把分类字段放在 JSON `type` 中。

后续可以同时增加：

```text
event: runtime.log
```

但前端仍应以 JSON `type` 为主，避免不同传输层差异影响渲染。

## 当前推送哪些事件

第一版只推关键摘要事件：

- `invocation.started`
- `invocation.completed`
- `invocation.failed`
- `agent.selected`
- `agent.enter`
- `agent.exit`
- `agent.error`
- `model.call.started`
- `model.call.completed`
- `model.call.error`
- `tools.bound`
- `tool.call`
- `tool.result.normalized`
- `tool.error`
- `skill.declared`
- `skill.resolved`
- `skill.injected`
- `skill.skipped`
- `skill.error`

高频或大体积事件不走 `runtime.log`：

- `openai.stream.chunk`
- `openai.request.payload`
- `openai.response.raw`
- `llm.request.final`
- `llm.response.partial`

这些仍然只进 JSONL trace。

## 后端实现

run_sse 在 resumable run producer 中临时追加一个 `runtimeLogSSERecorder`：

```text
FileRecorder       -> 写 .adk/data/traces/*.jsonl
runtimeLogRecorder -> 写 Redis SSE stream，前端实时消费
```

两者通过 `runtimetrace.MultiRecorder` 组合，不影响原有 trace。

## 前端实现

`ChatComponent` 收到：

```json
{"type":"runtime.log", ...}
```

会写入 `runtimeLogEvents`，不进入 `uiEvents` 聊天列表。

`RealtimeLogPanelComponent` 同时支持两种来源：

1. SSE 直接推送的 `runtime.log`；
2. 轮询 `/api/runtime/traces/{invocation_id}` 的历史 trace。

这样既能实时显示，也能在短暂断线后补齐部分日志。
