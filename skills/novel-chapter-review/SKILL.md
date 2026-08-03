---
name: novel-chapter-review
description: 按章节计划、场景卡、连续性、爽点、冲突和章尾钩子审查草稿，并输出严格 JSON 评审结果。
allowed-tools:
  - load_artifacts
  - save_artifact
metadata:
  display_name: 章节质量审稿
  language: zh-CN
  output_language: zh-CN
  stage: chapter_review
---
# 章节审稿 Skill

## 定位

你不改文，只做结构化审稿。目标是判断草稿是否完成章节计划、是否有具体爽点、是否推进故事、是否违背设定、是否需要重写。

## 工作步骤

1. 必须先调用 `list_artifacts`，确认输入产物是否存在；不要直接加载不存在的文件。
2. 必须读取：
   - `chapter_current_context_pack.json`
   - `chapter_current_plan.json`
   - `chapter_current_scenes.json`
   - `chapter_current_draft.md`
3. 如果 `chapter_current_draft.md` 缺失，但存在最近的 `chapter_XXX_draft.md`，可以读取最近的章节草稿并继续审稿。
4. 如果缺少计划、场景卡或上下文包，不要中断流程；必须保存一个保守失败的 `chapter_current_review.json`，其中：
   - `pass=false`
   - `must_rewrite=true`
   - `score` 不高于 60
   - `problems` 写明缺少哪些 artifact
   - `rewrite_instructions` 要求后续修订 Agent 只做轻量整理或请求补齐缺失产物
5. 输出并保存 `chapter_current_review.json`。若能确定章节号，也额外保存 `chapter_XXX_review.json`。
6. 保存 JSON 时 `mime_type` 使用 `application/json`。

## 保存硬性要求

- 无论审稿通过、失败、输入缺失还是证据不足，都必须调用 `save_artifact` 保存 `chapter_current_review.json`。
- 不允许只把 JSON 写在聊天回复里。
- 保存完成前不要结束本 Agent。

## 评分标准

总分 100。重点维度：

- `chapter_goal_completion`：是否完成本章目标。
- `conflict`：冲突是否具体、有利益来源、有升级过程。
- `payoff`：爽点是否落地，有没有具体收益或情绪峰值。
- `progression`：是否推动主线、关系、资源、线索或状态变化。
- `continuity`：是否承接既有设定，是否新增矛盾设定。
- `prose`：是否可读、节奏是否拖沓、对白是否有效。
- `ending_hook`：章尾是否让读者想看下一章。

`pass=true` 标准：总分 >= 82，且 `conflict`、`payoff`、`progression`、`continuity` 都 >= 75。

## JSON Schema

```json
{
  "chapter_no": 1,
  "pass": false,
  "score": 78,
  "must_rewrite": true,
  "scores": {
    "chapter_goal_completion": 0,
    "conflict": 0,
    "payoff": 0,
    "progression": 0,
    "continuity": 0,
    "prose": 0,
    "ending_hook": 0
  },
  "strengths": [],
  "problems": [],
  "rewrite_instructions": [],
  "continuity_risks": [],
  "do_not_change": []
}
```

## 审稿规则

- 不要为了鼓励而虚高评分。
- `problems` 必须具体到可修改问题。
- `rewrite_instructions` 必须能被修订 Agent 直接执行。
- `do_not_change` 用于保护草稿里已经有效的冲突、钩子或高光。
- 如果草稿偏离计划，即使文笔好也要扣分。
- 如果爽点只有口号，没有具体收益，`payoff` 不得高于 70。
