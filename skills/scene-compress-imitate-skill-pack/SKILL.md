---
name: scene-compress-imitate-skill-pack
description: 场景级压缩仿写实验室：从单章切出 500-1000 字场景，压缩成可仿写场景卡，隔离仿写，对比归因，生成运行时技能叠加上下文，最终沉淀为“怎么仿写、带什么技能”的技能包。
version: 0.1.0
tags:
  - novel
  - mcp
  - scene-lab
  - imitation
  - skill-overlay
  - skill-pack
visibility: private
---

# Scene Compress Imitate Skill Pack

## 一句话定位

本 Skill Pack 的最终产物不是“拆书报告”，而是一个可以交给写作 Agent 使用的 **仿写技能包**：

```text
给定一个场景压缩卡，应该怎么仿写？
仿写时要带哪些技能？
哪些技能经过对比验证真的能缩小差距？
```

## 核心链路

```text
已切分书籍章节
  -> 单章读取
  -> 场景切分
  -> 场景压缩卡
  -> 第一轮隔离仿写：无新增技能
  -> 差距归因
  -> 生成 Skill Overlay Context
  -> 第二轮隔离仿写：带技能上下文
  -> 二次差距归因
  -> 验证有效技能
  -> 沉淀 Final Imitation Skill Pack
```

## 角色边界

### Manager

Manager 不读章节正文，不写正文，不直接总结技能。它只负责：

- 选择书籍、章节、场景范围。
- 生成 `run_id`。
- 调度 scene splitter、writer、gap evaluator、distiller。
- 决定每一轮 writer 注入哪些 `skill_overlay_context`。
- 汇总 artifact ref，向用户返回最终技能包位置。

### Scene Splitter / Compressor

Scene Splitter 可以读取原文章节正文，但每次只处理一个章节或一个小范围章节。它负责：

- 按 500-1000 字左右切分自然场景。
- 不按固定字数硬切，优先按人物目标、地点、冲突阶段、信息释放节点切分。
- 为每个场景生成 `scene_compression_card`。
- 保存场景卡，禁止在主会话输出大段原文。

### Isolated Writer

Writer 不允许读取原文。它只看：

- `scene_compression_card`
- `active_skill_overlay_context`
- 基础仿写约束

Writer 的任务不是复刻原文，而是用同类功能、同类节奏、同类写作动作，写一个新场景。

### Gap Evaluator

Evaluator 可以读取：

- 原文场景或少量证据
- 场景压缩卡
- writer 草稿
- 本轮 skill overlay

它必须判断：差距来自压缩卡缺失、技能缺失、writer 执行不到位，还是风格参数不准。

### Skill Pack Distiller

Distiller 不写剧情。它只把多轮验证有效的 overlay 整理成最终技能包：

- 哪类场景使用。
- 先做什么，后做什么。
- 仿写时带哪些技能。
- 禁止什么写法。
- 验收标准是什么。

## 关键产物一：Scene Compression Card

`scene_compression_card` 是给 writer 的最小写作输入。它必须足够支持仿写，但不能暴露原文句子。

```json
{
  "run_id": "scene_lab_xxx",
  "book_id": "...",
  "chapter_no": 1,
  "scene_no": 1,
  "scene_label": "抽象场景名，例如：低位者被公开质疑后反压",
  "source_span": {
    "chapter_no": 1,
    "start_hint": "场景起点短说明，不摘长原文",
    "end_hint": "场景终点短说明，不摘长原文"
  },
  "scene_size": {
    "estimated_chars": 850,
    "paragraph_count": 12
  },
  "surface_facts": {
    "time": "时间状态",
    "place": "地点功能，不保留专有地名",
    "characters": [
      {
        "role_label": "高位压迫者/低位反击者/旁观见证者",
        "goal": "当前场景目标",
        "hidden_state": "隐瞒、试探、恐惧、算计等"
      }
    ],
    "event_goal": "这个场景要完成什么事件动作"
  },
  "conflict_engine": {
    "opening_pressure": "开场压力从哪里来",
    "power_relation": "谁掌握评价权、资源权、解释权",
    "tactics": ["试探", "压迫", "隐瞒", "反击", "转移话题"],
    "turning_point": "权力关系改变的那个动作",
    "closing_hook": "场景结尾把读者推向哪里"
  },
  "information_flow": [
    {
      "step": 1,
      "function": "先让读者知道什么",
      "withheld": "暂时不说什么",
      "reader_effect": "让读者产生什么期待/疑问/爽感"
    }
  ],
  "style_parameters": {
    "dialogue_ratio": "高/中/低，并说明原因",
    "narration_ratio": "高/中/低，并说明原因",
    "sentence_rhythm": "短句压迫/长句铺垫/短长交替",
    "detail_density": "动作细节/环境细节/心理细节的比例",
    "emotion_curve": "压抑 -> 试探 -> 反压 -> 留钩"
  },
  "imitation_brief": {
    "what_to_write": "换人物、换事件后要写的同类场景",
    "must_keep": ["场景功能", "权力变化", "信息释放顺序", "情绪曲线"],
    "must_change": ["人物名", "地点", "行业设定", "具体桥段", "表达句子"]
  },
  "candidate_skills": [
    {
      "skill_name": "动作级技能名",
      "trigger": "什么时候用",
      "procedure": ["步骤1", "步骤2", "步骤3"],
      "anti_pattern": "错误写法",
      "acceptance_check": "写完怎么验收"
    }
  ],
  "non_copy_constraints": [
    "不得复用原文人物名、地名、组织名、专有设定",
    "不得复用原文独创桥段组合",
    "不得复用连续句式、标志性比喻或关键台词"
  ]
}
```

## 关键产物二：Skill Overlay Context

`skill_overlay_context` 是运行时临时技能，不需要热改 YAML，也不需要立即写入正式 Skill。Manager 在第二轮 writer 调用时把它作为上下文注入。

```json
{
  "overlay_id": "scene_overlay_001_round2",
  "source": "gap_report_scene_001_round1",
  "scope": {
    "run_id": "scene_lab_xxx",
    "chapter_no": 1,
    "scene_no": 1,
    "round": 2
  },
  "priority": "high",
  "use_for": "下一轮同场景仿写",
  "skills": [
    {
      "skill_name": "先交出评价权，再用事实反压",
      "trigger": "场景中一方处于低位，被高位者公开质疑或压迫",
      "procedure": [
        "开场先让高位者掌握评价权或定义权",
        "低位者不要立刻解释，先用动作或沉默制造承压感",
        "再抛出一个具体事实，迫使高位者的判断失效",
        "最后让旁观者或环境反应确认权力发生偏移"
      ],
      "why_it_fixes_gap": "修复第一轮仿写中冲突太平、主角反击太直白的问题",
      "anti_pattern": "低位者一上来长篇解释，导致爽点变成说明文",
      "acceptance_check": "读者能看见评价权从高位者手里滑走，而不是只听作者说主角赢了"
    }
  ],
  "global_constraints": [
    "不能使用原文设定和台词",
    "技能只迁移动作结构，不迁移桥段内容",
    "每条技能必须在正文中有可见动作证据"
  ]
}
```

## 关键产物三：Scene Gap Report

Gap report 不是评分表，而是下一轮技能生成器。

```json
{
  "run_id": "scene_lab_xxx",
  "chapter_no": 1,
  "scene_no": 1,
  "round": 1,
  "score": {
    "scene_function": 7,
    "conflict_pressure": 5,
    "power_shift": 4,
    "information_release": 6,
    "style_execution": 5,
    "commercial_effect": 5
  },
  "root_causes": [
    {
      "rank": 1,
      "type": "skill_gap",
      "problem": "仿写只写出了事件，没有写出权力关系变化",
      "evidence_note": "草稿中双方只是争论，没有出现评价权转移动作",
      "needed_skill": "权力关系可视化反转"
    },
    {
      "rank": 2,
      "type": "execution_gap",
      "problem": "writer 知道要反击，但反击方式太直白",
      "evidence_note": "使用解释性独白替代了事实反压",
      "needed_skill": "用物证/旁观反应替代作者解释"
    }
  ],
  "skill_delta": {
    "add": [
      {
        "skill_name": "权力关系可视化反转",
        "trigger": "对话/冲突场景中需要制造爽点反压",
        "procedure": ["先给高位者定义权", "让低位者承压", "用具体事实反杀定义", "用第三方反应确认胜负"],
        "anti_pattern": "直接写主角很厉害、对方哑口无言",
        "acceptance_check": "不用作者解释，读者也能判断谁赢了"
      }
    ],
    "update": [],
    "avoid": [
      {
        "pattern": "说明文式反击",
        "reason": "它让冲突变成信息交代，削弱场面爽感"
      }
    ]
  }
}
```

## 关键产物四：Final Imitation Skill Pack

这是最终交付物。它回答两个问题：

1. 这个场景应该怎么仿写？
2. 仿写时要带哪些技能？

```json
{
  "skill_pack_id": "imitation_pack_power_reversal_scene_v001",
  "name": "低位反压型冲突场景仿写技能包",
  "source_free": true,
  "applicable_scene": {
    "scene_type": "低位者被高位者质疑、审问、羞辱、压迫后反压",
    "reader_effect": "先压住读者情绪，再用事实/动作/旁观反应释放爽点",
    "not_for": ["纯抒情场景", "无冲突过渡场景", "纯信息说明段落"]
  },
  "imitation_recipe": {
    "input_needed": [
      "时间地点人物",
      "谁拥有评价权/资源权/解释权",
      "低位者隐藏的信息或底牌",
      "高位者压迫动作",
      "反压证据",
      "结尾钩子"
    ],
    "scene_steps": [
      "开场建立公开压力，让高位者先定义局面",
      "低位者先承压，不急着解释，给一个动作或沉默",
      "插入旁观者反应，放大局势的不利感",
      "低位者抛出一个具体事实或动作，打坏高位者判断",
      "让高位者失去评价权，旁观者重新判断双方位置",
      "结尾留下更大问题或下一层压力"
    ]
  },
  "active_skills": [
    {
      "skill_name": "评价权先让后夺",
      "trigger": "需要制造先抑后扬的冲突爽点",
      "procedure": ["先让对方评价", "让主角承压", "用事实破坏评价", "让第三方确认反转"],
      "anti_pattern": "主角开场就解释清楚或直接碾压",
      "acceptance_check": "场景中能看见评价权转移"
    },
    {
      "skill_name": "用动作替代心理说明",
      "trigger": "角色承压、隐忍、试探时",
      "procedure": ["先写手部/目光/停顿", "再写一句短对白", "最后再给旁观反应"],
      "anti_pattern": "直接写他很紧张、他很愤怒、他很震惊",
      "acceptance_check": "删掉心理词后，读者仍能感到情绪"
    },
    {
      "skill_name": "结尾压力升级钩",
      "trigger": "本场冲突刚刚赢下一小步时",
      "procedure": ["给出小胜", "立刻暴露更高层级人物/规则/后果", "让胜利变成新危机入口"],
      "anti_pattern": "场景结束后完全松掉，没有下一步期待",
      "acceptance_check": "读者知道本场赢了，但更想看下一场怎么扛"
    }
  ],
  "quality_gate": {
    "must_pass": [
      "没有复用源书人物、设定、桥段和句子",
      "每条 active_skill 在正文中都有可见动作证据",
      "场景有明确权力变化，不只是事件发生",
      "结尾有继续阅读动力"
    ],
    "fail_if": [
      "只写剧情摘要，没有场面",
      "只写人物心理，没有动作证据",
      "反击靠解释完成",
      "爽点没有旁观者或局势反应承接"
    ]
  }
}
```

## 第一版运行策略

为了先跑通，不要一上来做全书。默认只跑：

```text
book_id + chapter_no=1 + max_scenes=3 + two_rounds=true
```

每个场景跑两轮：

```text
round 1：无 overlay，验证场景卡本身够不够。
round 2：带 overlay，验证技能补丁是否能缩小差距。
```

## 保存的 skill_type 约定

```text
scene_compression_card        场景压缩卡
scene_imitation_draft         隔离仿写稿
scene_imitation_gap_report    场景差距报告
scene_skill_overlay_context   运行时技能叠加上下文
scene_final_skill_pack        最终仿写技能包
```

`batch_no` 建议编码为：

```text
chapter_no * 10000 + scene_no * 100 + round_no
```

例如：

```text
10101 = 第 1 章，第 1 个场景，第 1 轮
10102 = 第 1 章，第 1 个场景，第 2 轮
10201 = 第 1 章，第 2 个场景，第 1 轮
```

## 质量底线

- 最终技能包必须 source-free，不出现原书书名、角色名、地名、组织名、章节号、book_id、run_id。
- 技能必须动作级，不接受“增强代入感、提升节奏、突出人物”这类空泛表述。
- 技能必须能被 writer 使用，也必须能被 evaluator 验收。
- 仿写不是复刻。只能迁移结构、功能、节奏、权力变化、信息释放，不迁移原文内容资产。
