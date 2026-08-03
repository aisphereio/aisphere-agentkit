# Analysis Run Runtime：真实子 Agent 并行执行方案

这个补丁解决的是：计划模式不能只靠提示词，必须有运行时能力把长任务拆到独立子 session 中执行。

## 关键变化

新增 `AnalysisRunToolset`：

- `analysis_run_start`：创建后台并行分析任务。
- `analysis_run_get`：查询任务进度。

它会通过 ADK REST API 创建独立 worker session，并调用指定 worker app，例如：

- `chapter_dialogue_worker_mcp`
- `dialogue_skill_distiller_mcp`

## 为什么这样做

不要让主 Agent 读取 100 章正文。主 Agent 只负责调用：

```text
analysis_run_start(book_id, start_chapter, end_chapter, concurrency)
```

然后后台启动多个 worker session：

```text
chapter_dialogue_worker_mcp / session: analysis_xxx_ch_0001
chapter_dialogue_worker_mcp / session: analysis_xxx_ch_0002
...
```

每个 worker 只读取一章，分析一章，保存一个 JSON 产物。

## 环境变量

```bash
export ADK_RUNTIME_BASE_URL=http://127.0.0.1:8080
export NOVEL_SPLITTER_MCP_ENDPOINT=http://127.0.0.1:8090/mcp
export NOVEL_SPLITTER_MCP_TOKEN=change-me
```

## Manager Agent 示例

使用 `dialogue_skill_manager_mcp`。

用户说：

```text
分析《大清首富》前100章，逐章分析里面的对话，提炼出一个如何写对话的skill。
```

Manager 应调用：

```json
{
  "name": "analysis_run_start",
  "arguments": {
    "book_id": "b_xxx",
    "book_name": "大清首富",
    "analysis_type": "dialogue",
    "start_chapter": 1,
    "end_chapter": 100,
    "concurrency": 8,
    "worker_app": "chapter_dialogue_worker_mcp",
    "final_app": "dialogue_skill_distiller_mcp"
  }
}
```

返回：

```json
{
  "run_id": "analysis_20260608_...",
  "status": "queued",
  "total": 100,
  "concurrency": 8
}
```

查询进度：

```json
{
  "name": "analysis_run_get",
  "arguments": {
    "run_id": "analysis_20260608_..."
  }
}
```

## 当前限制

这是 V1：

- run state 在当前进程内存中，服务重启会丢失。
- worker 是通过 ADK REST `/run` 调用的独立 session。
- 逐章结果保存依赖 `chapter_dialogue_worker_mcp` 调用 novel splitter 的 `save_skill_batch`。
- 后续可以把 run state 持久化到 DB / artifact / MinIO。

## 和 Plan Mode 的关系

Plan Mode 是 UI/提示层控制；AnalysisRunToolset 是真实执行层。

正确链路：

```text
UI 开启计划模式
  -> Manager Agent 生成计划
  -> analysis_run_start
  -> 后台并行 worker sessions
  -> 保存逐章分析产物
  -> final distiller 汇总
```
