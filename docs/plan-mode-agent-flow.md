# 前端计划模式与动态 Agent Flow 设计

## 目标

长任务（例如“分析前 100 章，逐章提炼对话 skill”）不能让主会话连续读取大量章节正文。计划模式用于让用户显式告诉 Agent：本轮请求应先规划，再启动可控的并行子任务，而不是在当前 session 中串行读全量上下文。

## 交互入口

前端聊天输入框左侧 `+` 菜单新增：

- 计划模式

开启后输入框上方显示“计划模式”状态 chip。发送消息时，前端会在 `/run_sse` 请求中增加：

```json
{
  "runMode": "plan",
  "planOptions": {
    "maxParallelAgents": 8,
    "defaultBatchSize": 1,
    "maxContextChars": 30000,
    "requirePlanBeforeExecution": true
  },
  "stateDelta": {
    "__adk_plan_mode__": true,
    "__adk_plan_options__": {
      "maxParallelAgents": 8,
      "defaultBatchSize": 1,
      "maxContextChars": 30000,
      "requirePlanBeforeExecution": true
    }
  }
}
```

## 后端行为

`RunAgentRequest` 新增：

```go
RunMode string `json:"runMode,omitempty"`
PlanOptions map[string]any `json:"planOptions,omitempty"`
```

当 `runMode=plan` 时，Runtime 会：

1. 写入 session state：`__adk_plan_mode__` 和 `__adk_plan_options__`。
2. 在本次消息前注入一段轻量控制指令 `[ADK_PLAN_MODE]...[/ADK_PLAN_MODE]`。
3. 控制指令要求 Agent：
   - 先规划再执行。
   - 不要在主会话读取大量章节或文件正文。
   - 长任务优先启动异步任务、analysis_run、子 Agent worker/reducer/distiller。
   - 正文只进入子 Agent 短会话。
   - 如果缺少并行任务工具，先输出可执行计划和缺口，不要串行读全量。

## 推荐 Agent 行为

用户：

```text
分析《大清首富》前100章，逐章分析里面的对话，提炼出一个如何写对话的 skill。
```

开启计划模式后，主 Agent 应该生成计划：

```json
{
  "task_type": "dialogue_skill_extract",
  "book_name": "大清首富",
  "range": {"start": 1, "end": 100},
  "granularity": "chapter",
  "concurrency": 8,
  "worker_agent": "chapter_dialogue_worker",
  "reducer_agent": "dialogue_segment_reducer",
  "final_agent": "dialogue_skill_distiller",
  "segment_size": 10
}
```

然后调用未来的任务启动工具，例如：

```text
start_analysis_run
```

而不是直接循环调用 `get_novel_chapter_batch(start=1,count=100)`。

## 边界

计划模式不是固定 Workflow。它只是一个用户显式开关，用来让 Agent 进入“动态计划 + 并行执行”的决策模式。

真正的并发、子 session、重试、状态管理，需要后续的 Analysis Run Runtime 支持，例如：

- `start_analysis_run`
- `get_analysis_run_status`
- `save_chapter_analysis`
- `get_chapter_analysis`
- `save_segment_analysis`
- `save_final_skill`

当前补丁先完成：

- UI 开关
- `/run_sse` 请求协议
- 后端计划模式指令注入
- 状态标记

