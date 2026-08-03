---
name: novel-chapter-reconstruction-eval
description: 对隔离重构样稿进行盲测评估，对照章节分析和技法包生成可用于迭代 Skill 的差距报告。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
  - files_retrieval
metadata:
  display_name: 章节技法盲测
  language: zh-CN
  output_language: zh-CN
  category: book_dissector
  artifact_protocol: reconstruction_gap_report
---
# 章节技法盲测评估 Skill

## 定位

本 Skill 用于评估“只给 brief + skill_pack 的隔离写作 Agent”是否能写出可读样稿，并把差距沉淀成可迭代的 Gap Report。评估目标不是让样稿复刻原文，而是判断当前技法包是否足够支撑同类章节写作。

## 输入材料

优先读取：

- `chapter_analysis_<book_id>_<chapter_index>.md`：源章节功能分析。
- `chapter_skill_pack_<book_id>_<chapter_index>.json`：抽象技法包。
- `reconstruction_probe_<book_id>_<chapter_index>_<attempt>.md`：隔离 Agent 生成的测试样稿。

允许补充：

- 阶段简介、人物状态简表、目标题材/风格说明。

禁止：

- 不要要求隔离 Agent 重新读取原文。
- 不要用“像不像原书”作为唯一标准。
- 不要把原书专有剧情、角色、设定直接写进改进建议。

## 评估维度

按五类 Gap 评估：

1. `brief_gap`：压缩 brief 没交代清楚目标、冲突、人物状态、结尾钩子。
2. `skill_gap`：technique 太空泛、步骤不可执行、缺少失败模式或迁移规则。
3. `context_gap`：缺少必要前情、人物关系、世界规则、资源状态。
4. `style_gap`：没有给出足够的视角、句式节奏、对白密度、情绪纹理。
5. `execution_gap`：隔离 Agent 执行偏差，明明 Skill Pack 有规则但没有遵守。

每项 0-5 分：

- 0：没有明显缺口。
- 1-2：轻微影响。
- 3：明显影响可读性或章节功能。
- 4-5：导致样稿核心失败。

## 输出产物

保存为：

```text
reconstruction_gap_report_<book_id>_<chapter_index>_<attempt>.json
```

结构：

```json
{
  "book_id": "string",
  "chapter_index": 1,
  "attempt": 1,
  "probe_artifact": "reconstruction_probe_...md",
  "skill_pack_artifact": "chapter_skill_pack_...json",
  "overall_result": "pass|partial|fail",
  "gap_scores": {
    "brief_gap": 0,
    "skill_gap": 0,
    "context_gap": 0,
    "style_gap": 0,
    "execution_gap": 0
  },
  "evidence": [
    {
      "gap_type": "skill_gap",
      "observation": "string",
      "suggested_fix": "string"
    }
  ],
  "decision": "accept_skill_pack|refine_brief|refine_skill_pack|add_context_pack|retry_probe|request_human_review",
  "skill_iteration_suggestions": [
    {
      "target": "technique.name 或 style_fingerprint 或 context_pack",
      "change": "string",
      "reason": "string"
    }
  ]
}
```

## 决策规则

- `accept_skill_pack`：样稿完成了章节功能，Gap 总体较低，可进入多章归纳或人工审核。
- `refine_brief`：主要失败来自 brief 过短或漏掉关键信息。
- `refine_skill_pack`：主要失败来自 technique 不可执行或过泛。
- `add_context_pack`：必须补充前情、人物关系、世界规则或资源状态。
- `retry_probe`：Skill Pack 足够，但执行 Agent 明显没遵守规则。
- `request_human_review`：证据不足，或涉及是否提升为公共 Skill 的判断。

## 人类审批边界

盲测结果只能生成改进建议或提案，不能自动发布新 Skill。把章节 Skill Pack 提升为通用 Skill 前，必须经过人类审核：是否通用、是否不侵权、是否没有过拟合原书、是否可被其他写作 Agent 使用。
