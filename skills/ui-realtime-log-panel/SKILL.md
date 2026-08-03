---
name: ui-realtime-log-panel
description: 定义前端实时运行日志面板协议，用于结构化展示 Agent、Tool、trace、审批和改进进度。
metadata:
  display_name: 实时运行日志面板
  language: zh-CN
  output_language: zh-CN
  category: frontend-observability
  tags: ui,tool-rendering,trace,logs,observability
---
# 实时运行日志面板 Skill

## 目标

当 Agent、Tool 或平台能力需要把执行过程展示给用户时，优先使用结构化运行事件，而不是把大量过程日志混入最终聊天正文。

前端会把 runtime trace 事件渲染到“实时运行日志”小窗口中，用于展示：

- Agent 进入/退出；
- 模型调用开始/完成；
- Tool 绑定、调用、返回、错误；
- Skill 注入、跳过、错误；
- Artifact 保存、读取；
- 审批等待、审批通过/拒绝；
- 自提升审查、改进提案生成过程。

## 适用场景

适合：

- 长任务运行过程；
- 多 Agent 工作流；
- 环境管理命令输出摘要；
- 工具调用状态；
- 审批流程状态；
- 自提升流程的审查、提案、审批包生成过程。

不适合：

- 最终业务结论；
- 用户必须阅读的确认问题；
- 大段完整命令输出；
- 大模型完整 prompt / response 原文。

大段原文应该进入 trace 文件、artifact 或对象存储，日志面板只显示摘要和引用。

## 事件设计规范

日志事件必须尽量结构化，避免只输出纯文本。

推荐事件名：

```text
invocation.started
invocation.completed
invocation.failed
agent.enter
agent.exit
agent.error
model.call.started
model.call.completed
model.call.error
tools.bound
tool.call
tool.result.normalized
tool.error
skill.injected
skill.skipped
artifact.save
approval.pending
approval.resolved
```

事件字段建议包含：

```json
{
  "run_id": "run_xxx",
  "invocation_id": "inv_xxx",
  "agent_name": "chapter_writer_agent",
  "type": "tool.call",
  "data": {
    "tool_name": "save_artifact",
    "message": "saving chapter_current_draft.md",
    "status": "running"
  }
}
```

## 输出原则

Agent 在最终回复里不需要重复所有过程日志，只需要给用户结论和下一步。

错误示例：

```text
我正在调用 load_artifacts...
load_artifacts 返回...
我正在调用 save_artifact...
save_artifact 返回...
```

正确示例：

```text
已完成章节草稿生成，并保存为 chapter_current_draft.md。关键执行过程可以在实时运行日志中查看。
```

## Tool 设计要求

Tool 返回结果应包含便于渲染的摘要字段：

- `message`：一句话说明；
- `status`：running / success / error / skipped；
- `resource_name` 或 `artifact_name`：关联资源；
- `duration_ms`：耗时；
- `error_message`：失败原因；
- `object_key` / `trace_id`：大对象引用。

不要把超长日志直接塞进 `message`。

## 审批与自提升流程

当自提升角色发现问题、生成提案、创建审批包时，也应该输出结构化事件，便于用户在小窗口中看到阶段进展：

```text
improvement.issue.created
improvement.proposal.created
improvement.approval_packet.created
```

最终变更必须由用户审核，不允许 Agent 直接修改生产 Agent / Skill / Tool 配置。
