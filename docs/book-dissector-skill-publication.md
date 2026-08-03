# 拆书教研候选如何真正落入 Skill

`chapter_skill_pack_*.json` 和 `cross_chapter_skill_candidates_*.json` 都只是教研产物。它们不会自动成为运行时 Skill，也不会自动被其他 Agent 加载。

真正落入 Skill 需要经过四步：

```text
cross_chapter_skill_candidates
  ↓ 选择稳定技法
生成正式 SKILL.md 草稿
  ↓ skill_validate_draft
skill_save_draft 保存到 skills root
  ↓ Admin 端人工审核 / 编辑 / 发布
把 skill name 加到目标 Agent 的 skills: 列表
```

## 为什么不能直接把 candidates 当 Skill

候选产物包含多个技法、证据、gap 信息和过拟合风险，它适合教研，不适合直接作为运行时提示词。正式 Skill 应该“一种稳定技法一个 Skill”，并且包含适用场景、执行步骤、失败模式、反过拟合规则和验收标准。

## 新增工具

`SkillAuthoringToolset` 提供：

- `skill_validate_draft`：校验一个待保存的 Skill 草稿。
- `skill_save_draft`：把草稿保存为真实 Skill，状态为 `draft` 或 `pending_review`。
- `skill_get_draft`：读取已保存的真实 Skill。
- `skill_list_drafts`：列出 Skill 仓库里的 Skill。

`skill_save_draft` 不会直接发布 `published`，发布必须走 Admin 端人工审核。

## 建议对 book_dissector 的用户指令

```text
请把 cross_chapter_skill_candidates 中的“阶级差异对白法”真正落入平台 Skill。

要求：
1. 读取 cross_chapter_skill_candidates 和相关 gap_report。
2. 只把“阶级差异对白法”做成一个独立 Skill，不要把多个技法混在一起。
3. 生成 Skill name：novel-dialogue-power-gap。
4. Skill 正文必须包含：适用场景、不适用场景、核心原理、执行步骤、示例模板、失败模式、反过拟合规则、验收标准、来源证据。
5. 先调用 skill_validate_draft。
6. 校验通过后调用 skill_save_draft，保存为 pending_review。
7. 保存 skill_publication_report，告诉我 Admin 端下一步怎么发布，以及要把哪个 skill name 加到写作 Agent 的 skills 列表。
```

## 落地后的检查

保存成功后，在 Admin 端 Skill 管理里应该看到新 Skill，例如：

```text
novel-dialogue-power-gap
status: pending_review
category: novel-writing
visibility: workspace
```

打开后确认正文没有原书私货，没有人物名绑定，没有“对白自然”这类口号。审核通过后点击发布。然后把 skill name 加到目标 Agent 的 YAML：

```yaml
skills:
  - novel-dialogue-power-gap
```

重启或重新加载 Agent 后，该 Skill 才会进入运行时可用列表。
