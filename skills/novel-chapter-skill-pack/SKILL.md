---
name: novel-chapter-skill-pack
description: 从已拆解的网文章节中提炼边界清楚、可测试、可复用的单章写作技法包。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
  - files_retrieval
metadata:
  display_name: 章节技法包提炼
  language: zh-CN
  output_language: zh-CN
  category: book_dissector
  artifact_protocol: chapter_skill_pack
---
# 小说章节技法包 Skill

## 定位

本 Skill 用于把已经拆解过的章节，压缩成“另一个写作 Agent 可以执行”的通用技法包。它不是剧情总结，也不是复刻原书的提示词，而是把章节里的写法抽象成可迁移、可盲测、可迭代的写作动作。

## 输入边界

允许输入：

- `book_id`、章节序号、章节标题。
- 当前章节文本或由 `book_dissector` 产出的章节分析。
- 可选的上一章尾部摘要、下一章开头摘要、阶段简介、人物状态摘要。

禁止输入：

- 不要把原文章节大段复制进输出。
- 不要输出原书专有设定、专有角色、专有剧情作为“通用技法”。
- 不要把“都市赘婿被羞辱后反打某某人”这种具体桥段当成 Skill。
- 不要只写“节奏快、人物丰满、对白自然”这种无法执行的评价。

## 提炼尺度

Skill Pack 的粒度必须介于“泛泛原则”和“单章私货”之间：

- 太大：`写好人物`、`制造爽点`、`增加代入感`，不可用。
- 太小：`让张三在饭店被李四嘲讽后反击`，不可迁移。
- 合适：`公开场合压制→隐藏筹码延迟揭示→用证据反转→反转后追加更大压力`。

一个章节可以产出多个 technique，但每个 technique 必须能迁移到同类型章节。多章归纳时，优先保留反复出现、跨章节有效的技法，丢弃只服务单一桥段的细节。

## 输出产物

保存为：

```text
chapter_skill_pack_<book_id>_<chapter_index>.json
```

必须是 JSON，结构如下：

```json
{
  "book_id": "string",
  "chapter_index": 1,
  "chapter_title": "string",
  "source_artifacts": ["chapter_analysis_..."],
  "compressed_brief": "100-200 Chinese characters",
  "character_state": [
    {
      "name": "string",
      "visible_goal": "string",
      "hidden_pressure": "string",
      "relationship_delta": "string"
    }
  ],
  "scene_contract": {
    "opening_hook": "string",
    "central_conflict": "string",
    "turning_point": "string",
    "ending_hook": "string"
  },
  "techniques": [
    {
      "name": "string",
      "purpose": "string",
      "execution_steps": ["string"],
      "success_signals": ["string"],
      "failure_modes": ["string"],
      "transfer_scope": "适合哪些章节/题材/情绪场",
      "anti_overfit_rule": "如何避免复刻原书桥段"
    }
  ],
  "style_fingerprint": {
    "pov": "string",
    "sentence_rhythm": "string",
    "dialogue_ratio": "string",
    "sensory_texture": "string",
    "tension_pattern": "string"
  },
  "context_pack": {
    "required": false,
    "items": ["string"]
  },
  "quality_bar": {
    "must_be_reusable": true,
    "must_be_executable": true,
    "must_avoid_plot_copy": true
  }
}
```

## 技法质量标准

每个 technique 必须回答：

1. 解决什么写作问题。
2. 适合什么章节位置和情绪场。
3. 执行时先做什么、后做什么、在哪里转折、在哪里释放。
4. 写成功时读者会看到什么信号。
5. 写失败时会变成什么问题。
6. 如何迁移到别的题材，而不是复刻原书。

## 多章归纳规则

当输入来自两章或三章时，不要简单合并所有技法。必须做筛选：

- 至少两章都出现的结构动作，优先提升为稳定技法。
- 只在一章有效但强度很高的动作，可以标记为 `candidate`。
- 依赖特定人设、设定、地名、道具、身份的内容，不能直接进入通用 technique，只能转写为抽象关系或冲突机制。

## 盲测要求

Skill Pack 产出后，必须能交给一个隔离的写作 Agent。隔离 Agent 只拿到：

- `compressed_brief`
- `scene_contract`
- `techniques`
- `style_fingerprint`
- 可选的极小 `context_pack`

如果隔离 Agent 写不出可读样稿，要区分是 brief 不足、context 不足、style 不足，还是 technique 不够可执行。
