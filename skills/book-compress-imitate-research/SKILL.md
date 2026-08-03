---
name: book-compress-imitate-research
description: 从已切分书籍的 MCP 章节数据中，分批压缩结构与知识点，生成隔离仿写 brief，再通过原文/仿写差距归因提炼写作 Skill。
version: 0.1.0
tags:
  - novel
  - mcp
  - compression
  - imitation
  - skill-distillation
visibility: private
---

# Book Compress Imitate Research Skill

## 定位

本 Skill 用于已经切分好的书籍资产。正文来源必须是 Novel Splitter / novel_assets MCP，不接受用户上传全文，不在 Agent 内重新切书。

目标链路：

```text
已切分书籍章节
  -> 批次压缩
  -> 知识点/结构/风格指纹合并
  -> 隔离仿写
  -> 原文/仿写差距归因
  -> 写作 Skill 增量
```

## 核心原则

1. Manager 不读正文，只查书籍元数据和调度 worker。
2. Compression worker 只读一个章节批次，并把结果保存成 `compress_imitation_batch`。
3. Distiller 只读批次小结果，不读章节正文。
4. Writer 只读 distilled package，不读原文。
5. Evaluator 可以读取 distilled、draft、batch 小结果；必要时少量读取原文样本，但不得输出大段原文。
6. 正式 Skill 正文必须 source-free：不含书名、角色名、地名、组织名、章节号、book_id、run_id。
7. 证据只进入 metadata / evidence_index / gap_report，不进入通用 Skill 正文。

## 批次压缩卡

每个 batch 必须输出以下结构：

```json
{
  "run_id": "...",
  "book_id": "...",
  "batch_no": 1,
  "chapter_range": {"start": 1, "end": 3},
  "structure_brief": [
    {
      "unit": "场景或段落功能",
      "purpose": "这一段在整体推进中的作用",
      "cause_effect": "前因后果",
      "turning_point": "转折点",
      "closing_hook": "收束或钩子"
    }
  ],
  "knowledge_points": [
    {
      "point": "可迁移知识点或判断",
      "support_logic": "它在文本中如何被证明或展开",
      "implicit_premise": "读者默认要接受的前提",
      "reuse_note": "新文本如何复用"
    }
  ],
  "style_fingerprint": {
    "sentence_rhythm": "句段长度和停顿方式",
    "dialogue_density": "对白/叙述比例",
    "explanation_density": "解释密度",
    "emotion_curve": "情绪变化",
    "information_release": "信息释放方式"
  },
  "skill_candidates": [
    {
      "name": "动作级技法名",
      "use_when": "适用场景",
      "steps": ["步骤1", "步骤2", "步骤3"],
      "anti_pattern": "错误写法",
      "acceptance_check": "验收标准"
    }
  ],
  "evidence_index": [
    {
      "chapter_no": 1,
      "scene_label": "抽象场景标签",
      "evidence_note": "极短证据说明，不摘抄长原文"
    }
  ],
  "non_copy_constraints": [
    "不得复用原文人物名",
    "不得复用原文专有设定",
    "不得复用原文独创桥段或句子"
  ]
}
```

## Distilled Package

合并后的 `compress_imitation_distilled` 必须包含：

- `compressed_brief`：给仿写用的全范围压缩，不是剧情梗概。
- `knowledge_map`：知识点、判断链、信息层级、隐含前提。
- `structure_map`：章节/段落/场景功能序列。
- `style_fingerprint`：可执行的风格参数。
- `imitation_brief`：给 Writer 的隔离任务书。
- `skill_candidates`：按优先级排序的可迁移动作级技能。
- `non_copy_constraints`：禁止复用的原文元素。

## 隔离仿写规则

Writer 只能看 distilled package，不能看原文。

仿写迁移对象：

- 结构顺序。
- 信息释放方式。
- 场景功能。
- 节奏参数。
- 知识组织方式。
- 对白/叙述比例。
- 情绪曲线。

禁止迁移对象：

- 原文人物名。
- 原文地名、组织名、专有名词。
- 原文独创设定。
- 原文桥段组合。
- 原文句子、比喻、标志性表达。

## 差距归因维度

Gap evaluator 必须按以下维度判断：

1. `brief_gap`：压缩 brief 信息是否缺失或方向错误。
2. `knowledge_gap`：知识点抽象是否太粗、太碎或误解。
3. `structure_gap`：场景功能序列、铺垫兑现、转折点是否弱。
4. `style_gap`：句段节奏、对白密度、解释密度、情绪曲线是否偏离。
5. `execution_gap`：Writer 是否没有执行 brief。
6. `skill_gap`：现有 skill 缺少哪些动作级规则。
7. `copy_risk`：是否过度贴近原文。

## Skill Delta 格式

`compress_imitation_skill_delta` 应输出：

```json
{
  "add": [
    {
      "skill_name": "新增动作级技能",
      "trigger": "什么时候用",
      "procedure": ["步骤1", "步骤2", "步骤3"],
      "why_it_fixes_gap": "它修复哪个差距",
      "acceptance_check": "怎么判断写对了"
    }
  ],
  "update": [
    {
      "target_skill": "已有技能名",
      "change": "如何改",
      "reason": "为什么要改"
    }
  ],
  "remove_or_avoid": [
    {
      "pattern": "应该避免的错误模式",
      "reason": "为什么会导致仿写变差"
    }
  ]
}
```

## 质量底线

- 不能把每章摘要拼起来冒充压缩。
- 不能把形容词列表冒充风格指纹。
- 不能把评论式建议冒充 Skill。
- 每条技能必须有触发条件、步骤、反例和验收标准。
- 任何面向运行时写作 Agent 的 Skill 正文都必须脱离源书可复用。
