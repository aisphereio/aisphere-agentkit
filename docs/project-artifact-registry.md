# Project Artifact Registry：项目制产物交接 MVP

这一版把“切书 Agent 的产物交给 Skill Runner”从聊天上下文里移出来，落到项目资产注册表里。

核心原则：

```text
Session = 一次短执行现场，可以丢弃
Project = 长期工作台
Artifact = 真实产物文件
Project Artifact Registry = 项目里有哪些产物、谁生产、谁可见、谁默认挂载
```

## 新增 Toolset

新增 `ProjectArtifactToolset`，暴露：

- `project_workspace_create`：创建/更新项目，并写入 `mounted_project.json`。
- `project_workspace_mount`：在新 session 中挂载已有项目。
- `project_workspace_get`：读取项目注册表。
- `project_artifact_register`：把已有 artifact 登记为项目产物。
- `project_artifact_list`：按类型、可见性、生产 Agent、默认 Agent 等过滤产物。
- `project_artifact_update`：修改展示名、描述、可见性、默认挂载 Agent。
- `project_artifact_defaults`：获取某个 Agent 默认应看到的项目产物。

注册表 artifact 命名：

```text
user:project__<project_id>__artifacts.json
```

当前 session 挂载指针：

```text
mounted_project.json
```

## 可见性

| visibility | 含义 |
|---|---|
| `session_private` | 当前 session 私有草稿，不参与项目交接 |
| `project_visible` | 项目内可见，用户可以选择挂载 |
| `project_default` | 项目默认产物，新 session / 指定 Agent 优先使用 |
| `system_hidden` | 状态指针、run state 等系统恢复文件，默认不展示 |
| `published` | 已沉淀成正式 Skill 或可发布资产 |

## book_dissector 自动注册

`book_split_from_artifact` / `book_resplit` / `book_apply_manual_boundaries` 保存切章结果后，会自动创建或更新项目注册表：

- `book.source`：UTF-8 规范化原文，`project_visible`
- `book.chapter_manifest`：章节索引，`project_default`，默认给 `book_dissector` 和 `book_skill_runner`
- `book.chapter`：单章正文，`project_visible`，默认给 `book_skill_runner`

如果用户没有传 `project_id`，默认使用 `book_id` 作为项目 ID。

## book_skill_runner 自动注册

`book_skill_run_start` 创建 run 后自动注册：

- `run.state`：长任务状态，`system_hidden`
- `run.latest`：最新 run 指针，`system_hidden`

`book_skill_run_record_batch(status=completed)` 会把本批产物注册进项目：

- `batch.analysis`：批次分析，`project_visible`
- `skill.delta`：Skill 增量，`project_visible`
- `skill.version`：合并后的完整 Skill 版本，`project_default`
- `skill.evaluation`：质量检查，`project_visible`

Book-to-Skill 迭代不再通过新增 `v001`、`v002` 或章节范围后缀文件表达历史。四类产物使用稳定 artifact 名：

```text
user:book_skill_batch_analysis__<book_id>__<run_id>.md
user:book_skill_delta__<book_id>__<run_id>.json
user:book_skill__<book_id>__<run_id>.md
user:book_skill_eval__<book_id>__<run_id>.json
```

每轮保存到同名 artifact，由 artifact service 产生新版本。项目注册表只保留一组项目入口，并在 metadata 中记录最新 `artifact_version` 和 `latest_batch`。

## 推荐交接流程

### 1. 切书

```text
用 book_dissector 上传并切书。
```

切完后检查：

```text
project_artifact_list(types=["book.chapter_manifest", "book.chapter"])
```

### 2. 新 session 继续

```text
project_workspace_mount(project_id="...")
project_artifact_defaults(agent_id="book_skill_runner")
```

然后 `book_skill_runner` 使用默认资产继续，不需要重新切书。

### 3. 每批 Skill 迭代

```text
book_skill_run_next_batch
读取当前章节
保存 analysis / delta / skill version / evaluation
book_skill_run_record_batch(status="completed")
```

完成后项目资产中会自动更新同一个 `skill.version` 产物的最新版本，不会为每轮新增一个 Skill 文件。

## 前端展示建议

项目页面可以按类型分组：

- 书籍原文
- 章节索引
- 章节正文
- 长任务状态
- 批次分析
- Skill 增量
- Skill 版本
- 质量检查

每个卡片展示：

```text
标题 / 类型 / 生产 Agent / visibility / mountable / default_for_agents / artifact_name
```

按钮：

```text
查看
设为默认
隐藏
取消隐藏
新 session 使用
复制 artifact ref
发布为 Skill
```
