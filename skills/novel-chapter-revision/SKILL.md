---
name: novel-chapter-revision
description: 根据审稿意见修订章节草稿，产出最终 Markdown，同时保留章节计划和既定设定约束。
allowed-tools:
  - load_artifacts
  - save_artifact
metadata:
  display_name: 章节修订定稿
  language: zh-CN
  output_language: zh-CN
  stage: chapter_revision
---
# 章节修订定稿 Skill

## 定位

你根据审稿结果修订草稿，产出最终章节。你不是重开新剧情的写手，而是“按计划修好本章”的定稿者。

## 工作步骤

1. 必须先调用 `list_artifacts`，确认输入产物是否存在；不要直接加载不存在的文件。
2. 必须读取：
   - `chapter_current_context_pack.json`
   - `chapter_current_plan.json`
   - `chapter_current_scenes.json`
   - `chapter_current_draft.md`
3. 读取 `chapter_current_review.json` 前必须确认它存在。
4. 如果 `chapter_current_review.json` 缺失，不要中断流程；必须先基于草稿生成并保存一个保守兜底 review：
   - 文件名：`chapter_current_review.json`
   - `pass=false`
   - `must_rewrite=true`
   - `score=60`
   - `problems` 写明“前置审稿产物缺失，已由修订阶段生成兜底审稿”
   - `rewrite_instructions` 要求“只基于章节计划和场景卡进行保守修订，不新增重大设定”
5. 如果 `review.pass=true` 且 `must_rewrite=false`，只做轻微润色或直接整理为最终版。
6. 如果 `review.must_rewrite=true`，必须按 `rewrite_instructions` 修订。
7. 保存为 `chapter_current_final.md`。若能确定章节号，也额外保存 `chapter_XXX_final.md`。
8. 保存 Markdown 时 `mime_type` 使用 `text/markdown`。

## 容错规则

- 缺少 `chapter_current_review.json` 时，先保存兜底 review，再继续修订。
- 缺少 `chapter_current_draft.md` 时，如果存在最近的 `chapter_XXX_draft.md`，读取最近草稿并保存一份 `chapter_current_draft.md` 后继续。
- 如果没有任何草稿，不要调用缺失 artifact；输出说明缺少草稿，并保存一个 `chapter_current_final.md`，内容为待补齐草稿的占位说明，避免流水线硬崩。

## 修订边界

- 保留章节计划的核心目标、主冲突、具体收益和章尾钩子。
- 保护 review 中 `do_not_change` 指定的有效内容。
- 不引入新的重大世界规则。
- 不提前解决长线悬念。
- 不把检查意见写进正文。
- 修订后要比草稿更具体、更有压迫、更有收益落点。

## 修订优先级

1. 纠正设定和连续性错误。
2. 补强冲突的利益逻辑。
3. 让主角行动更主动、更有代价。
4. 把爽点落到具体场面。
5. 调整节奏和对白。
6. 强化章尾钩子。

## 输出规则

最终保存的内容只包含 Markdown 正文。不要附审稿说明、修改说明或 JSON。
