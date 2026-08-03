# Book-to-Skill 长任务 MVP

这个补丁把“拆书切章”和“Skill 螺旋迭代”拆开：

- `BookPreprocessorToolset` 继续负责确定性切章、manifest、章节读取和跨 session 挂载。
- `BookSkillRunToolset` 新增负责长任务状态、章节批次计划、Skill 版本产物命名、暂停/恢复/列表。
- `book_dissector` 和新的 `book_skill_runner` Agent 负责调用这些工具，模型只做章节分析和 Skill 写作，不负责保存进度。

## 为什么这样做

不要把 100 章放进一个 session。session 只是一轮短执行现场；长期状态必须落到 user-scoped artifacts。

每 5 章一批的流程是：

```text
book_skill_run_start
  ↓
book_skill_run_next_batch
  ↓
book_get_chapter 读取当前批次章节
  ↓
save_artifact 保存 batch analysis
  ↓
save_artifact 保存 skill delta
  ↓
save_artifact 保存下一版 skill version
  ↓
save_artifact 保存 evaluation
  ↓
book_skill_run_record_batch(status=completed)
```

换新 session 后：

```text
book_list_books / book_mount
  ↓
book_skill_run_get 或 book_skill_run_list
  ↓
book_skill_run_next_batch
```

## 新增工具

`BookSkillRunToolset` 暴露这些工具：

- `book_skill_run_start`：创建一个 durable run，按章节范围和 batch_size 生成所有批次。
- `book_skill_run_get`：读取指定 run，或读取当前书的 latest run。
- `book_skill_run_next_batch`：返回下一批章节范围和目标 artifact 名，可标记为 running。
- `book_skill_run_record_batch`：记录本批 completed / failed / skipped，并推进当前 Skill 版本。
- `book_skill_run_pause`：暂停、恢复、失败或完成一个 run。
- `book_skill_run_list`：列出当前 artifact workspace 里的 Book-to-Skill runs。

## 关键 artifact

状态文件都是 `user:` 作用域，跨 session 可见：

```text
user:book_skill_run__<run_id>__state.json
user:<book_id>__book_skill_run_latest.json
```

每批工具会给出确定性产物名：

```text
user:book_skill_batch_analysis__<book_id>__<run_id>.md
user:book_skill_delta__<book_id>__<run_id>.json
user:book_skill__<book_id>__<run_id>.md
user:book_skill_eval__<book_id>__<run_id>.json
```

这些是 canonical artifact 名。每一批仍然保存到同一个 artifact；artifact service 负责产生新的版本号。run state 会记录每一批对应的 `*_artifact_version`，项目资产注册表只保留一组“当前 Skill / 批次分析 / 增量 / 质量检查”入口，避免平台里堆满 `v001`、`v002` 或 `0001_0005` 这样的文件。

## 使用建议

第一次跑：

```text
“基于当前挂载的书，创建一个拆书 Skill 长任务，每 5 章一批，从 1 到 100 章。”
```

继续下一批：

```text
“继续当前拆书 Skill 长任务的下一批。”
```

换新 session 继续：

```text
“挂载这本书，继续上次的 Book-to-Skill run。”
```

最终完成后，让 Agent 加载 `current_skill_artifact`，做一次最终整理，然后通过 `SkillAuthoringToolset` 保存为 `pending_review` 的真实 Skill 草稿。

## 项目资产交接补充

本流程现在不再依赖“上一个 session 的聊天上下文”交接切书产物。`book_dissector` 切书后会把源文、章节索引、章节正文登记到 `ProjectArtifactToolset` 维护的项目资产注册表中。

`book_skill_runner` 启动时建议先执行：

```text
project_workspace_mount(project_id="...")
project_artifact_defaults(agent_id="book_skill_runner")
```

然后再 `book_skill_run_get` / `book_skill_run_next_batch`。这样新 session 只需要 project_id，就能恢复章节索引、最新 Skill 版本和 run state 指针。

每次 `book_skill_run_record_batch(status=completed)` 之后，批次分析、Skill delta、Skill version、evaluation 会自动注册为项目产物。`skill.version` 使用 `project_default`，用于下一轮或新 session 默认加载。注册表里的 `metadata.artifact_version` 指向最新 artifact 版本；历史轮次通过 artifact versions 查看，而不是新增文件名。
