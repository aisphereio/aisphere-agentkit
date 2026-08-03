# 实时运行日志面板

## 背景

聊天框适合承载用户消息和最终回答，不适合承载大量 Agent 执行过程、Tool 调用细节、模型调用状态和环境命令摘要。

本设计新增一个通用前端组件：`RealtimeLogPanelComponent`。它像一个小型运行终端，实时显示当前会话最近几次 invocation 的 runtime trace 事件。

## 当前实现

前端新增：

```text
agentkit-web/src/app/components/realtime-log-panel/
  realtime-log-panel.component.ts
  realtime-log-panel.component.html
  realtime-log-panel.component.scss
```

集成位置：

```text
agentkit-web/src/app/components/chat-panel/chat-panel.component.html
```

当前组件复用已有后端接口：

```http
GET /api/runtime/traces/{invocation_id}?limit=300
```

它会根据当前 `uiEvents` 和 `traceData` 中的 invocation id 自动轮询最近几次调用，并展示：

- 事件时间；
- 事件类型；
- Agent 名称；
- 摘要消息；
- 错误/警告/成功状态。

## 为什么先用轮询

第一版不新增 WebSocket，不改 SSE 协议，直接复用 runtime trace JSONL 文件和已有 API。

这样改动小、风险低，且能立刻验证体验。

后续可以升级为：

```text
run_sse -> 同时推送 runtime log event -> 前端 LogPanel 实时消费
```

或：

```text
GET /api/platform/runs/{run_id}/logs/stream
```

## 与聊天框的边界

聊天框展示：

- 用户输入；
- Agent 最终回答；
- 关键交互卡片；
- 用户必须处理的确认事项。

实时日志面板展示：

- Agent 生命周期；
- Tool 调用；
- Skill 注入；
- 模型调用；
- Artifact 保存；
- 审批状态；
- 自提升流程阶段。

## 与 Skill / Tool 的关系

新增 Skill：

```text
skills/ui-realtime-log-panel/SKILL.md
```

它约束 Agent 和 Tool 设计者：

- 过程日志尽量结构化；
- 大段日志不要塞进最终回答；
- Tool result 应提供 `message`、`status`、`duration_ms`、`artifact_name` 等摘要字段；
- 大对象进入 artifact/object store，日志面板只展示摘要和引用。

## 后续优化

建议下一步：

1. 后端增加标准 `runtime.log` SSE 事件；
2. `run_sse` 将关键 trace event 同步推给前端；
3. LogPanel 支持按 Agent / Tool / Error 过滤；
4. LogPanel 支持点击事件查看 JSON 详情；
5. 接入 PG `run_steps`，刷新后可恢复运行日志；
6. 环境管理 Tool 增加专用日志渲染器，展示命令、风险等级、审批、输出摘要。


## P4.2 runtime.log SSE

在轮询 runtime trace 的基础上，`run_sse` 会实时推送标准日志事件：

```json
{
  "type": "runtime.log",
  "run_id": "...",
  "invocation_id": "...",
  "agent_name": "...",
  "event_type": "tool.call",
  "data": {}
}
```

前端收到 `type=runtime.log` 时只更新实时日志窗口，不追加到聊天消息列表。这样聊天框继续承载用户消息和最终回答，运行过程进入专用日志面板。
