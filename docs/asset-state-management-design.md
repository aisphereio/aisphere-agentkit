# Asset State Management Design

## 背景

当前平台已经有 upload、artifact、project artifact、book split、plan run、book skill run 等能力，但它们还没有统一成一个工程化的资产状态系统。结果是：

- 同一份上传文本可能被多个 Agent 重复 attach、UTF-8 规范化、切章。
- 切书完成后，章节、manifest、project registry 分散在 artifact 版本里，前端容易把 session 产物、user 产物、历史版本混在一起展示。
- Project registry 作为一个 JSON artifact 被频繁整包保存，一本 259 章的书可能产生数百个 registry 版本。
- 长任务如果中断，理论上已有 `PlanRunToolset` / `BookSkillRunToolset` 状态，但还缺统一的 checkpoint / idempotency 协议来保证所有任务都从上次进度继续。
- LLM 有时参与了不该参与的确定性步骤，例如是否重新切分、是否重新格式化，而不是先由程序检查状态。

平台应该把每一步处理都视为一个可查询、可复用、可恢复的资产派生动作。

## 核心原则

1. **每一步都是资产**
   上传原文、UTF-8 规范化文本、章节索引、章节正文视图、Skill 迭代结果、评估报告、长任务 checkpoint 都是资产。

2. **每个资产都有状态**
   资产不只是文件，还应该有 `status`、`version`、`content_hash`、`producer`、`inputs`、`params_hash`、`created_at`、`updated_at`、`error`。

3. **派生动作必须幂等**
   同一个输入、同一组参数、同一个 processor version 已经产出成功资产时，后续任务直接复用，不重复执行。

4. **确定性动作由程序执行**
   编码识别、UTF-8 规范化、切章候选、manifest 生成、章节 offset 查询、文件 hash、版本检查、状态推进都应该由代码做。

5. **不确定决策才让模型参与**
   章节边界冲突、重复标题判断、切章策略选择、Skill 抽象、质量评估、gap 归因可以让模型参与，但模型必须基于程序给出的候选、上下文和状态，而不是自由猜。

6. **长任务必须可恢复**
   长任务每轮写 checkpoint。服务中断后继续执行时，从最新 completed checkpoint 之后开始，而不是从 0 开始。

7. **展示层只看索引，不扫对象存储**
   前端产物管理应查询 DB 里的资产索引和当前版本，不直接扫 filesystem / MinIO object list。

## 推荐架构

```text
PostgreSQL
  assets
  asset_versions
  asset_edges
  processing_jobs
  project_assets
  book_assets
  book_chapters
  plan_runs
  plan_run_steps

MinIO / S3
  raw uploaded files
  normalized text
  large artifact content
  trace blobs
  generated reports

Redis
  processor locks
  resumable run cursors
  short-lived queue state
  SSE buffers
```

PostgreSQL 是事实库。MinIO/S3 是对象内容仓库。Redis 是运行时协调层。本地 filesystem 只保留为 dev adapter。

## 资产模型

### assets

一条逻辑资产，例如“某上传文件的 UTF-8 正规化文本”或“某书的章节索引”。

```text
id
tenant_id
project_id
type
name
display_name
scope              -- upload / project / session / system
status             -- pending / processing / ready / failed / superseded / archived
current_version_id
content_hash
params_hash
producer_kind      -- deterministic_tool / llm_agent / human
producer_id
processor_version
created_at
updated_at
```

### asset_versions

一条资产的不可变版本。

```text
id
asset_id
version
object_key
mime_type
size_bytes
content_hash
metadata_json
created_by_run_id
created_by_step_id
created_at
```

### asset_edges

资产派生关系，用于判断是否已有结果、能否复用、如何回溯。

```text
from_asset_id
from_version_id
to_asset_id
to_version_id
edge_type          -- derived_from / overrides / evaluates / summarizes
```

### processing_jobs

确定性处理或 LLM 处理都登记为 job。

```text
id
job_type           -- normalize_utf8 / split_book / extract_skill_batch
input_fingerprint
params_hash
status             -- queued / running / completed / failed / cancelled
locked_by
started_at
finished_at
error
output_asset_ids
```

`input_fingerprint + params_hash + processor_version` 应有唯一约束。这样天然避免重复跑。

## 派生状态机

```text
missing
  -> pending
  -> processing
  -> ready
  -> superseded
  -> archived

processing
  -> failed
  -> ready

failed
  -> pending
```

调用任何 processor 前，先查状态：

1. 已有 `ready` 且输入/参数/processor_version 一致：直接返回 existing asset。
2. 已有 `processing`：返回 job 状态，必要时等待或提示正在处理。
3. 已有 `failed`：根据 retry policy 决定重试或要求用户确认。
4. 已有 `ready` 但参数不同：创建新版本或新派生资产，并把旧资产标记为 superseded。

## 上传到切书流程

### 1. Upload

用户上传后创建：

```text
asset type = upload.raw
status = ready
content_hash = sha256(file)
object_key = uploads/{tenant}/{upload_id}/raw
```

同一用户同一 hash 的文件可以直接复用，避免重复上传。

### 2. UTF-8 规范化

处理前查询：

```text
job_type = normalize_utf8
input = upload.raw current version
params = encoding_policy + newline_policy
processor_version = normalize-text/v1
```

如果已存在成功输出：

```text
asset type = text.normalized_utf8
status = ready
```

则直接复用，不再格式化。

### 3. 切章预览

切章预览是确定性 processor：

```text
job_type = split_book_preview
input = text.normalized_utf8
params = split_rules
processor_version = book-splitter/vN
```

输出：

```text
asset type = book.split_preview
```

如果 preview 有风险，例如重复标题、缺章、短章、异常编号跳跃，则进入 `needs_decision`。

### 4. 人或 AI 决策

只有当程序发现不确定情况时，才让 LLM 或用户参与：

```text
decision type = chapter_boundary_decision
input = split_preview + prev_tail + candidate + next_head
output = decision artifact
```

决策本身也是资产，后续可复用和审计。

### 5. 切章提交

提交后不要默认保存每章全文 artifact。建议保存：

```text
asset type = book.source_utf8
asset type = book.manifest
DB table book_chapters:
  book_id
  chapter_index
  title
  start_offset
  end_offset
  status
  override_asset_version_id
```

`book_get_chapter` 按 offset 从 source 读取正文。只有用户修改某章或生成人工修订时，才保存 chapter override asset。

这样一本 259 章的书不会生成 259 个章节文件，也不会因为 project registry 反复保存产生数百个版本。

## 长任务状态管理

长任务不应该依赖聊天上下文。它应该依赖 durable run + durable steps。

### plan_runs

```text
id
project_id
run_type           -- book_skill_loop
objective
status             -- running / paused / completed / failed
cursor             -- next batch index or next chapter index
max_iterations
completed_iterations
created_at
updated_at
```

### plan_run_steps

```text
id
plan_run_id
step_index
input_asset_versions
output_asset_versions
status             -- pending / running / completed / failed / skipped
checkpoint_json
started_at
finished_at
```

恢复逻辑：

1. 查询 `plan_runs.status in running/paused`。
2. 找到最后一个 `completed` step。
3. 从 `cursor` 或 `last_completed + 1` 继续。
4. 如果上一次停在 `running` step，检查 output assets 是否完整；完整则补记 completed，不完整则重跑该 step。

示例：处理 200 章，每 5 章一批，已完成 150 章。

```text
plan_run.cursor = batch 31
completed_iterations = 30
last completed chapter range = 146-150
continue => next batch 151-155
```

模型不应该自己猜“从哪里继续”。工具必须直接返回下一批。

## LLM 与程序边界

| 步骤 | 执行者 | 是否可重复 | 说明 |
|---|---|---:|---|
| 文件 hash | 程序 | 否 | 已有 hash 直接复用 |
| UTF-8 规范化 | 程序 | 否 | 输入/参数一致则复用 |
| 切章候选 | 程序 | 否 | 规则和版本一致则复用 |
| 重复标题风险判断 | 程序 + 可选 LLM | 有条件 | 程序先检测，LLM 只处理 ambiguous case |
| 切章确认 | 用户或策略 | 否 | 决策入库 |
| 章节读取 | 程序 | 否 | offset read，不让模型读全书 |
| 每批章节分析 | LLM | 是 | 有 batch checkpoint |
| Skill 合并 | LLM | 是 | 输出版本化 skill asset |
| 质量评估 | LLM + 程序 schema 校验 | 是 | 结构化评估 |
| 状态推进 | 程序 | 否 | LLM 不直接改 cursor |

## 幂等接口形态

所有 processor 工具建议统一支持：

```json
{
  "input_asset_id": "...",
  "params": {},
  "reuse_existing": true,
  "force": false,
  "dry_run": false
}
```

返回：

```json
{
  "status": "ready",
  "reused": true,
  "job_id": "...",
  "asset_id": "...",
  "asset_version": 3,
  "reason": "same input_hash + params_hash + processor_version"
}
```

如果用户明确要求重跑：

```json
{
  "force": true,
  "reason": "user changed split rule"
}
```

重跑应该产生新版本或新派生资产，并把旧版本保留可回滚。

## 前端展示原则

前端产物管理不应展示对象存储物理文件，也不应平铺所有 artifact versions。

默认展示：

- project current assets
- asset current version
- status
- type
- producer
- updated_at
- 是否可挂载给当前 Agent

默认隐藏：

- session_private
- system_hidden
- superseded
- old versions
- intermediate registry blobs

用户点击资产后再展开：

- versions
- lineage
- source inputs
- processor logs
- related decisions
- download / preview

## 迁移计划

### Phase 1: 状态协议止血

- 给现有 tool 输出增加 `reused`、`status`、`asset_version`、`input_hash`、`params_hash`。
- `book_split_commit` 前先查是否已有同 hash + rules 的 split result。
- `book_skill_run_next_batch` 必须只返回下一个未完成 batch。
- 前端默认只展示 project registry 最新态，不展示 registry artifact 历史版本。

### Phase 2: Project registry 入库

- 新增 `project_assets` 表。
- `ProjectArtifactToolset` 改为 DB repository。
- 保留 JSON artifact registry 只作为兼容导出，不再作为事实源。

### Phase 3: Book asset 入库

- 新增 `books`、`book_chapters`、`book_split_jobs`。
- 切书不再默认保存每章 artifact，改为 source + offset index。
- `book_get_chapter` 改为 DB index + object range read。

### Phase 4: MinIO/S3 内容存储

- artifact version content 写 MinIO。
- DB 存 object key、hash、mime、size、version。
- filesystem adapter 只用于本地开发。

### Phase 5: Processor orchestration

- 增加统一 `AssetProcessorService`。
- 支持 lock、retry、resume、force rerun、dry run、decision required。
- 所有工具通过 processor service 执行确定性步骤。

## 验收标准

- 同一上传文件重复进入流程时，不会重复 UTF-8 规范化。
- 同一 normalized text + 同一切章规则不会重复切章。
- 重切必须说明参数变化或用户 force reason。
- 一次 200 章长任务中断后，可以从第 151 章继续，不从 0 开始。
- 前端项目产物列表不会因为 artifact versions 显示出 500+ 个“章节”。
- 用户能看到每个资产从哪里来、由谁生成、当前版本是什么、是否可回滚。
- LLM 不再负责猜下一步确定性流程，只负责需要语义判断和写作抽象的部分。
