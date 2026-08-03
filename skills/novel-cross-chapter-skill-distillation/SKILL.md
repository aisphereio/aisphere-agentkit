---
name: novel-cross-chapter-skill-distillation
description: 把多章技法包归纳成更稳定的可复用写作技法候选，供人工审核后发布为正式 Skill。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
  - files_retrieval
metadata:
  display_name: 多章技法归纳
  language: zh-CN
  output_language: zh-CN
  category: book_dissector
  artifact_protocol: cross_chapter_skill_candidates
---
# 小说多章技法归纳 Skill

## 定位

本 Skill 用于把多个 `chapter_skill_pack` 归纳成“可候选升级为正式写作 Skill”的通用技法集合。它不是把单章技法拼接起来，而是做教研筛选：哪些技法跨章节稳定成立，哪些只是单章局部技巧，哪些是原书私货必须剔除。

## 输入

优先读取：

- 至少 2 个 `chapter_skill_pack_<book_id>_<chapter_index>.json`。
- 可选的 `reconstruction_gap_report_<book_id>_<chapter_index>_<attempt>.json`。
- 可选的阶段简介、章节简介、人物状态摘要。

禁止：

- 不要读取整本书或大段原文来做“印象式总结”。
- 不要把原书角色、组织、道具、地名、具体桥段提升为通用 Skill。
- 不要把所有 technique 全部合并。多章归纳的价值在于筛选和降噪。

## 目标卡对齐

如果上游 Book-to-Skill run state 中存在训练目标卡，归纳时必须只围绕该目标筛选技法，不要额外泛化出整本书风格。

例如目标是 `novel-dialogue-power-gap` 时，只筛选与“对白如何体现权力差”有关的结构动作：

- 对白长度不对称。
- 回应速度不对称。
- 命令权、评价权、沉默权、打断权。
- 旁观者反应如何确认权力秩序。

不要把历史背景、商业制度、角色关系、具体桥段提升为该 Skill 的主体。

候选技法中应补充：`skill_focus`、`target_technique`、`abstraction_level`、`transfer_scope`、`source_specific_details_rejected`、`runtime_body_policy`、`evidence_policy`。

## 归纳尺度

一个技法要进入 `stable_techniques`，必须满足：

1. 至少两个章节或两个盲测样本中出现相同结构动作。
2. 可以用抽象关系表达，而不是依赖原书专有名词。
3. 有明确执行步骤，另一个写作 Agent 可以照着写。
4. 有适用边界，不能被包装成“万能写法”。
5. 有反过拟合规则，防止复刻原书剧情。

只在单章出现但有潜力的，放进 `candidate_techniques`，标记 `needs_more_samples`。

## 输出产物

保存为：

```text
cross_chapter_skill_candidates_<book_id>_<start>_<end>.json
```

结构：

```json
{
  "book_id": "string",
  "start_chapter": 1,
  "end_chapter": 3,
  "source_skill_packs": [
    "chapter_skill_pack_<book_id>_1.json",
    "chapter_skill_pack_<book_id>_2.json"
  ],
  "stable_techniques": [
    {
      "name": "string",
      "skill_focus": "dialogue",
      "target_technique": "通过对白体现权力差",
      "abstraction_level": "atomic_technique",
      "transfer_scope": "不限源书题材，可迁移到任意权力不对等场景",
      "source_specific_details_rejected": ["源书角色名", "源书地点名", "源书组织名", "章节号"],
      "runtime_body_policy": "source_free",
      "evidence_policy": "metadata_only",
      "evidence_chapters": ["1", "2"],
      "general_pattern": "抽象结构动作，不含原书私货",
      "execution_steps": ["step 1", "step 2"],
      "applicability": "适用题材、章节位置、情绪场",
      "anti_overfit_rule": "如何避免复刻原书角色、场景、道具",
      "validation_status": "ready_for_review"
    }
  ],
  "candidate_techniques": [
    {
      "name": "string",
      "skill_focus": "dialogue",
      "target_technique": "通过对白体现权力差",
      "abstraction_level": "atomic_technique",
      "transfer_scope": "不限源书题材，可迁移到任意权力不对等场景",
      "source_specific_details_rejected": ["源书角色名", "源书地点名", "源书组织名", "章节号"],
      "runtime_body_policy": "source_free",
      "evidence_policy": "metadata_only",
      "evidence_chapters": ["3"],
      "general_pattern": "string",
      "execution_steps": ["step 1", "step 2"],
      "applicability": "string",
      "anti_overfit_rule": "string",
      "validation_status": "needs_more_samples"
    }
  ],
  "rejected_overfit_details": ["被剔除的原书私货或过小桥段"],
  "upgrade_recommendations": ["下一步建议"],
  "human_review_required": true
}
```

## 决策规则

- `stable_techniques` 不是正式 Skill，只是“可进入人工审核”的候选。
- 如果某个技法在盲测中连续出现 `skill_gap >= 3`，不要提升为 stable，先回到单章 `chapter_skill_pack` 修改执行步骤。
- 如果主要问题是 `brief_gap` 或 `context_gap`，不要误判成 Skill 不行，应补 `compressed_brief` 或 `context_pack`。
- 如果 `style_gap` 稳定出现，可以新增“风格指纹 Skill 候选”，但不要混入剧情结构 Skill。

## 输出口径

面向用户时，用教研视角汇报：

1. 哪些技法跨章稳定。
2. 哪些只是候选，需要更多样本。
3. 哪些内容因为过拟合原书被剔除。
4. 哪些技法建议进入人工审核。
5. 下一轮盲测应该怎么做。
