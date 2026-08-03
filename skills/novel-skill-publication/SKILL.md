---
name: novel-skill-publication
description: 把拆书验证过的技法候选整理成真实可复用的 ADK 写作 Skill 草稿，避免照搬原书剧情和设定。
metadata:
  display_name: 技法发布成 Skill
  language: zh-CN
  output_language: zh-CN
version: 0.1.0
status: published
visibility: workspace
category: novel-writing
labels:
  - novel
  - skill-research
  - skill-publication
tags:
  - novel
  - writing
  - skill
  - publication
---
# 小说技法发布 Skill

## 目标

你负责把 `cross_chapter_skill_candidates_*.json`、`chapter_skill_pack_*.json` 和 `reconstruction_gap_report_*.json` 中经过验证的技法，整理成真正可复用的 ADK Skill 草稿。

注意：`cross_chapter_skill_candidates` 只是教研候选产物，不是正式 Skill。真正落入 Skill 系统，必须生成一个真实的 `SKILL.md` 草稿，并调用 `skill_save_draft` 保存到平台 Skills 仓库。保存后，它会出现在 Admin 端 Skill 管理中，等待人工审核和发布。

## 发布边界

### 允许发布为 Skill 的内容

只允许发布满足这些条件的技法：

1. 至少有 2 个章节证据，或者 1 个章节证据 + 1 次有效盲测证明。
2. 能抽象为通用写作动作，不依赖原书人物、专有设定、特殊桥段。
3. 有明确适用场景、执行步骤、失败模式、反过拟合规则和验收标准。
4. 能被另一个写作 Agent 读取后执行，而不是只有抽象口号。
5. gap_report 没有显示主要问题来自 brief_gap/context_gap/style_gap；如果主要失败不是 skill_gap，不要急着改 Skill。

### 禁止发布为 Skill 的内容

禁止把下面内容直接发布为通用 Skill：

- 某本书的剧情复刻。
- 某个角色、势力、地点、历史背景的私有设定。
- “对白自然”“人物丰满”“节奏紧凑”这类口号。
- 只在单章出现且未盲测的桥段技巧。
- 没有失败模式和验收标准的写作建议。

## Skill 拆分原则

不要把多个异质技法塞进一个巨大 Skill。优先“一种稳定动作 = 一个 Skill”。

例如：

- `阶级差异对白法` 可以单独成为 `novel-dialogue-power-gap`。
- `隐藏实力式对话法` 可以单独成为 `novel-dialogue-hidden-strength-reversal`。
- `轻描淡写爆信息法` 如果证据不足，先保留为 candidate，不要发布。

## 正式 Skill 草稿结构

生成的 Skill 正文必须包含以下章节。注意：正式 `SKILL.md` 正文必须是运行时方法论，不是教研记录；来源证据只能放入 metadata、evaluation 或 publication report，不能放入正文。

```markdown
# <中文显示名>

## 适用场景

说明这个 Skill 适合什么冲突、什么人物关系、什么叙事任务。不要绑定源书题材。

## 不适用场景

说明什么时候不要用，避免滥用。

## 核心原理

用 3-5 句话解释这个技法为什么有效。

## 执行步骤

给出可操作步骤，每一步都必须是写作动作，不要写抽象口号。

## 示例模板

给一个不绑定原书的抽象模板。只能使用“上位者/下位者/对手/旁观者/资源持有者/被评价者”等功能角色。

## 失败模式

列出常见写崩方式。

## 反过拟合规则

明确哪些原书细节必须丢弃，保留什么抽象动作。

## 验收标准

给出写完后检查清单。

```

## 正文去来源化规则

正式 Skill 正文禁止出现：

- 书名、作者名、原书角色名、原书地点名、原书专有组织名。
- 章节号、批次号、book_id、project_id、run_id。
- `source_artifacts`、`evidence_chapters`、`chapter_skill_pack_*`、`cross_chapter_skill_candidates_*`、`reconstruction_gap_report_*` 等 artifact 痕迹。
- “从《某书》提炼”“基于某章案例”“来源证据如下”这类教研说明。

这些信息只能进入：

- `skill_save_draft.source_artifacts`
- `skill_save_draft.evidence_chapters`
- metadata
- evaluation / publication report

## 权力差对白发布目标卡

当用户要求提炼“权力对话 / 权力差对白”时，优先按下面目标发布，不要泛化成整本书风格总结：

```json
{
  "name": "novel-dialogue-power-gap",
  "display_name": "权力差对白法",
  "skill_focus": "dialogue",
  "target_technique": "通过对白长度、回应速度、命令权、评价权和旁观者反应体现人物权力差",
  "abstraction_level": "atomic_technique",
  "transfer_scope": "都市、玄幻、职场、家族、门派、官场、商战等任意权力不对等场景",
  "runtime_body_policy": "source_free",
  "evidence_policy": "metadata_only",
  "non_goals": [
    "不提炼源书题材背景",
    "不提炼源书人物关系设定",
    "不复述章节剧情",
    "不写成历史商战综合写作指南"
  ]
}
```

## 保存规则

生成草稿后必须先调用：

```text
skill_validate_draft
```

验证通过后再调用：

```text
skill_save_draft
```

`skill_save_draft` 只保存为 `draft` 或 `pending_review`，不能直接发布为 `published`。正式发布必须由 Admin 端人工审核。

## 命名规则

Skill name 必须是英文小写短横线格式：

```text
novel-dialogue-power-gap
novel-dialogue-hidden-strength-reversal
novel-scene-public-pressure-reversal
```

不要使用中文、空格、下划线或书名。

## 保存参数建议

调用 `skill_save_draft` 时建议：

```json
{
  "name": "novel-dialogue-power-gap",
  "description": "Use asymmetric dialogue and response speed to reveal power distance between characters.",
  "version": "0.1.0",
  "status": "pending_review",
  "visibility": "workspace",
  "category": "novel-writing",
  "tags": ["novel", "dialogue", "power-gap", "generated", "skill-research"],
  "labels": ["小说", "对白", "阶级差", "教研生成"],
  "source_artifacts": ["cross_chapter_skill_candidates_...json", "reconstruction_gap_report_...json"],
  "evidence_chapters": ["2", "3"]
}
```

## 回复用户时

保存成功后，告诉用户：

1. 已生成真实 Skill 草稿，不再只是 artifact。
2. Skill name 是什么。
3. 当前状态是 `pending_review` 或 `draft`。
4. 可以在 Admin 端 Skill 管理里打开、编辑、发布。
5. 这个 Skill 还没有自动挂到写作 Agent；发布后需要把 skill name 加到目标 Agent 的 `skills:` 列表。
