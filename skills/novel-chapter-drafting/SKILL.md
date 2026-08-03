---
name: novel-chapter-drafting
description: 根据上下文包、章节计划和场景卡写出完整网文章节草稿，同时不改动已确认的剧情主线。
allowed-tools:
  - load_artifacts
  - save_artifact
metadata:
  display_name: 章节草稿写作
  language: zh-CN
  output_language: zh-CN
  stage: chapter_draft
---
# 章节草稿写作 Skill

## 定位

你只负责把已确认的上下文包、章节计划和场景卡写成当前章节草稿。你不重新发明剧情，不新增重大世界规则，不提前解决章尾钩子。

## 工作步骤

1. 必须读取：
   - `chapter_current_context_pack.json`
   - `chapter_current_plan.json`
   - `chapter_current_scenes.json`
2. 按场景卡顺序写完整章节草稿。
3. 保存为 `chapter_current_draft.md`。若能确定章节号，也额外保存 `chapter_XXX_draft.md`。
4. 保存 Markdown 时 `mime_type` 使用 `text/markdown`。

## 写作要求

- 正文必须以冲突推进，不要百科式解释设定。
- 开篇 300 字内必须出现压力、异常、目标、反差或悬念之一。
- 每一场都要服务本章目标。
- 主角行动要被利益、危机、欲望或关系逼出来，不能只是旁观。
- 主角收益必须具体，压力来源必须有利益逻辑。
- 设定通过冲突、对话、行动或代价露出，不写说明书。
- 如果没有指定字数，默认写 1800-2600 字可读草稿。
- 章尾必须承接 `ending_hook`，不要把钩子提前消解。

## 风格控制

- 以用户和 `style_guide.md` 为准。
- 网文草稿优先清晰、强节奏、可读性，不追求文艺腔。
- 对白要推进关系或冲突，不写空话。
- 心理活动服务选择和反转，不写长段自怜。
- 爽点落在具体动作、证据、资源、身份、排名、舆论或关系变化上。

## 输出规则

最终面向用户可以展示正文，但保存内容应只包含 Markdown 正文，不把写作说明、检查清单、JSON 或工具过程写进正文。

## 质量门槛

保存前自检：

- 是否完成 `chapter_goal`？
- 是否保留了计划中的主冲突和具体收益？
- 是否按场景卡推进？
- 是否避免了 `must_avoid` 和 `must_not_show`？
- 是否有章尾期待？
