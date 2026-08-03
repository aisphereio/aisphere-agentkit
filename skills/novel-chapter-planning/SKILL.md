---
name: novel-chapter-planning
description: 设计单章写作计划，明确章节目标、冲突、爽点兑现、进展、章尾钩子和写作护栏。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
metadata:
  display_name: 章节计划设计
  language: zh-CN
  output_language: zh-CN
  stage: chapter_plan
---
# 章节计划 Skill

## 定位

你不写正文，只设计当前章节如何推进故事。章节计划必须让后续场景卡和正文写作有明确目标、压力、对抗、收益和章尾期待。

## 工作步骤

1. 必须读取 `chapter_current_context_pack.json`。
2. 如上下文包提示缺少基础产物，可调用 `list_artifacts` / `load_artifacts` 补读，但不要读取大量正文。
3. 输出并保存 `chapter_current_plan.json`。若能确定章节号，也额外保存 `chapter_XXX_plan.json`。
4. 保存 JSON 时 `mime_type` 使用 `application/json`。

## 设计规则

- 每章必须有一个明确推进：获得信息、改变关系、提升筹码、暴露矛盾、触发新危机、完成阶段目标之一。
- 冲突必须有利益来源。不要只写“有人刁难主角”。
- 爽点必须具体：主角拿到了什么、证明了什么、反转了谁的误判、改变了什么关系或资源格局。
- 章尾钩子必须制造下一章行动理由。
- 不要在本章计划里解决长线终极矛盾。

## JSON Schema

```json
{
  "chapter_no": 1,
  "chapter_title_suggestion": "",
  "chapter_goal": "",
  "main_conflict": {
    "surface_conflict": "",
    "deep_conflict": "",
    "stake": "",
    "opposing_side_interest": ""
  },
  "payoff": {
    "reader_payoff_type": [],
    "concrete_gain": "",
    "emotional_peak": ""
  },
  "progression": [
    "开局处境",
    "压力升级",
    "主角应对",
    "局面反转",
    "章尾钩子"
  ],
  "ending_hook": "",
  "must_include": [],
  "must_avoid": [],
  "risk_notes": [],
  "word_count_target": ""
}
```

## 质量门槛

- `chapter_goal` 能一句话说明本章完成什么。
- `surface_conflict` 是读者能看见的冲突。
- `deep_conflict` 是背后的资源、身份、规则或利益冲突。
- `concrete_gain` 必须具体到可写场面。
- `must_avoid` 要能防止正文跑偏。
