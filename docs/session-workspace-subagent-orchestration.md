# Session Workspace + Sub Agent Orchestration

## 目标

本补丁把“拆书训练 Skill”的运行方式收敛成：

```text
novel-splitter MCP：只提供小说资产能力
ADK Runtime：管理 session / workspace / run state
Manager Agent：查书、拆批次、派发、验收
Worker Agent：读取小批量章节、分析、写临时产物
Session Workspace：保存临时产物，用户确认后再发布
Skill Service：只保存最终发布后的正式 Skill
```

## MCP 服务边界

小说 MCP 服务只应该暴露资产能力：

```text
list_split_books
get_book_info
list_novel_chapters    # 必须分页，Manager 不使用
get_novel_chapter      # 单章，Worker 可选
get_novel_chapter_batch
create_split_job
get_job_status
split_novel_from_object_key
```

不应该由 MCP 管：

```text
get_split_handoff      # 全书交接清单，容易爆上下文
save_skill_batch
list_skill_batches
get_skill_batch
merge_skill_batches
```

Skill 产物属于 ADK Runtime / Artifact / Skill Service。

## Session Workspace 生命周期

每个 ADK session 一个临时 workspace：

```text
.adk/data/workspaces/sessions/<session_id>/
  runs/<run_id>/
    run_state.json
    batch_0001/
      batch_summary.json
      batch_analysis.md
      skill_delta.json
      current_skill.md
    current/
      current_skill.md
      index.json
```

临时产物不会自动发布。用户确认后才发布：

```text
.adk/data/workspaces/published/<project_id>/<run_id>/
```

用户丢弃后删除当前 run 文件夹。

## Manager / Worker 关系

### Manager

只负责：

```text
list_split_books
session_workspace_init_run
调度 dialogue_skill_batch_worker
验收 worker 返回摘要
publish/discard
```

Manager 不读章节正文，不查目录全集，不加载历史 artifact 正文。

### Worker

只负责一个 batch：

```text
get_novel_chapter_batch(book_id, start, count<=10)
逐章提炼 micro-skill
写入 worker session workspace
commit 指定产物回 parent session workspace
```

Worker 使用 fresh context，避免把章节正文污染 Manager session。

## Workspace 共享策略

本补丁采用 `fork_commit`：

```text
Manager session workspace = 父工作区
Worker session workspace = 子工作区
Worker 完成后只 commit 白名单文件：
  batch_summary.json
  batch_analysis.md
  skill_delta.json
  current_skill.md
```

禁止提交：

```text
raw_chapters.json
chapter_text/*
```

## 测试流程

1. 重启 novel-splitter MCP。
2. 重启 ADK 后端。
3. 新建前端 session，选择 `dialogue_skill_manager`。
4. 发送：

```text
唐骑，看前 10 章，逐章提取对话相关内容，总结成通用 skill。
```

预期：

```text
Manager 调 list_split_books 找 book_id。
Manager 初始化 session run。
Worker 调 get_novel_chapter_batch 读取 1-10 章。
Worker 写 workspace 产物。
Worker commit 四个产物回 parent workspace。
Manager 只返回摘要和 workspace refs。
```

## 清理旧 session

旧 session 如果已经塞入过全书目录或大工具结果，必须删除，否则会继续爆上下文：

```powershell
Remove-Item -Recurse -Force .\.adk\data\sessions -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force .\.adk\data\traces -ErrorAction SilentlyContinue
```
