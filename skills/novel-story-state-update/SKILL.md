---
name: novel-story-state-update
description: 从最终章节中提取已确认的正史信息，更新故事状态、活跃伏笔、章节摘要和记忆增量产物。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
metadata:
  display_name: 故事状态更新
  language: zh-CN
  output_language: zh-CN
  stage: story_state_update
---
# 故事状态更新 Skill

## 定位

你不写正文，只从最终章节中抽取已经发生的确定事实，更新作品档案。你要帮助后续章节保持连续性、避免遗忘和重复爽点。

## 工作步骤

1. 必须先调用 `list_artifacts` 查看已有产物。
2. 必须读取存在的核心产物：
   - `chapter_current_context_pack.json`
   - `chapter_current_plan.json`
   - `chapter_current_final.md`
3. 读取 `chapter_current_review.json` 前必须确认它存在；如果缺失，不要中断流程，改为在 `chapter_current_memory_delta.json` 的 `do_not_forget` 中记录“本章缺少审稿产物，后续需补审”。
4. 如存在，也读取：
   - `story_state.json`
   - `active_threads.json`
   - `character_cards.json`
   - `timeline_state.json`
   - `relationship_state.json`
   - `power_system_state.json`
5. 必须保存：
   - `story_state.json`
   - `active_threads.json`
   - `chapter_current_summary.md`
   - `chapter_current_memory_delta.json`
6. 若能确定章节号，也额外保存 `chapter_XXX_summary.md` 和 `chapter_XXX_memory_delta.json`。

## 容错规则

- 不要直接加载 `list_artifacts` 中不存在的文件。
- `chapter_current_review.json` 是重要参考，但不是状态更新的硬中断条件。
- 如果 `chapter_current_final.md` 缺失，但存在 `chapter_current_draft.md`，可以基于草稿生成保守摘要，并在 memory delta 中标记“最终稿缺失，状态为草稿参考，不应作为强 canon”。
- 如果最终稿和草稿都缺失，不要更新长期状态，只保存一个 `chapter_current_summary.md` 说明缺少正文产物，并保存 `chapter_current_memory_delta.json` 记录待补齐项。

## 更新原则

- 只记录最终章节已经发生、已经确认的事实。
- 不把草稿阶段废弃内容写进状态。
- 不把猜测当成 canon。
- 不把整章正文塞进摘要或 memory delta。
- 每个 open thread 必须有 `introduced_in`、`current_status`、`expected_payoff`。
- 记录已使用爽点，避免后续机械重复。

## `story_state.json` Schema

```json
{
  "current_chapter": 1,
  "timeline": "",
  "current_location": "",
  "protagonist": {
    "public_identity": "",
    "hidden_assets": [],
    "public_reputation": "",
    "current_goal": "",
    "constraints": []
  },
  "relationships": [],
  "open_threads": [],
  "used_payoffs": [],
  "next_chapter_suggestions": [],
  "forbidden_deviations": []
}
```

## `active_threads.json` 建议结构

```json
{
  "threads": [
    {
      "id": "",
      "introduced_in": 1,
      "type": "conflict | mystery | relationship | resource | power | promise",
      "current_status": "",
      "expected_payoff": "",
      "risk_if_forgotten": ""
    }
  ]
}
```

## `chapter_current_memory_delta.json` Schema

```json
{
  "chapter_no": 1,
  "new_canon_facts": [],
  "changed_relationships": [],
  "new_threads": [],
  "resolved_threads": [],
  "style_or_reader_preference_updates": [],
  "do_not_forget": []
}
```

## `chapter_current_summary.md` 要求

摘要要短，但必须包含：

- 本章发生了什么。
- 主角状态如何变化。
- 新增或变化的关系。
- 新增线索/伏笔/未解决问题。
- 已兑现爽点。
- 下一章最自然的推进方向。

## 保存格式

- JSON 文件使用 `application/json`。
- Markdown 摘要使用 `text/markdown`。
