---
name: novel-chapter-context-pack
description: 把已保存的小说项目产物压缩成短小、可靠的 JSON 上下文包，供单章写作流水线使用。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
  - files_retrieval
metadata:
  display_name: 章节上下文打包
  language: zh-CN
  output_language: zh-CN
  stage: chapter_context_pack
---
# 章节上下文打包 Skill

## 定位

你只负责把当前章节需要的可靠上下文压缩成结构化 JSON。你不写正文，不做世界观扩写，不把大量旧正文塞进上下文。

## 读取策略

1. 必须先调用 `list_artifacts` 查看已有产物。
2. 优先读取项目基础产物：
   - `opening_intake_brief.md`
   - `premise_design.md`
   - `worldbuilding_design.md`
   - `story_bible.md`
   - `character_cards.json`
   - `style_guide.md`
   - `reader_payoff_strategy.md`
   - `forbidden_deviations.md`
   - `story_state.json`
3. 若存在，也读取状态类产物：
   - `active_threads.json`
   - `timeline_state.json`
   - `relationship_state.json`
   - `power_system_state.json`
4. 章节承接优先读取最近的 `chapter_*_summary.md`。只有摘要不足时，才读取最近的 `chapter_*_final.md`，并只提炼必要事实。
5. 不要把整章正文复制进 context pack。

## 章节号判断

- 用户明确说“第一章/第二章/下一章”时，以用户为准。
- 否则从 `story_state.current_chapter + 1` 推断。
- 仍无法判断则默认为 1。

## 输出与保存

必须保存 `chapter_current_context_pack.json`。若能确定章节号，也额外保存 `chapter_XXX_context_pack.json`，`XXX` 用三位数字。

保存 JSON 时 `mime_type` 使用 `application/json`。

## JSON Schema

```json
{
  "chapter_no": 1,
  "user_request": "",
  "project_positioning": {
    "channel": "",
    "genre": [],
    "tone_style": "",
    "target_reader_payoff": []
  },
  "confirmed_canon": {
    "premise": "",
    "world_rules": [],
    "power_or_system_rules": [],
    "protagonist_status": {},
    "important_relationships": [],
    "forbidden_deviations": []
  },
  "recent_story": {
    "previous_chapter_summary": "",
    "current_location": "",
    "timeline": "",
    "open_threads": []
  },
  "chapter_need": {
    "must_advance": [],
    "must_payoff": [],
    "must_not_do": [],
    "suggested_conflict_sources": []
  },
  "writing_constraints": {
    "word_count_target": "",
    "pov": "",
    "style_notes": [],
    "continuity_warnings": []
  }
}
```

## 质量门槛

- `confirmed_canon` 只放已经确认的设定，不放猜测。
- `chapter_need.must_advance` 必须能指导本章推进。
- `must_payoff` 必须是具体读者收益，不写“爽点拉满”。
- `continuity_warnings` 必须提醒可能写偏的地方。
- 上下文包要短、准、可执行。
