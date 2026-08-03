---
name: novel-scene-design
description: 把章节计划拆成 3 到 5 张因果相连的场景卡，明确冲突、转折、收益和钩子。
allowed-tools:
  - load_artifacts
  - save_artifact
metadata:
  display_name: 场景卡设计
  language: zh-CN
  output_language: zh-CN
  stage: scene_design
---
# 场景卡设计 Skill

## 定位

你不写正文，只把章节计划拆成 3 到 5 个可直接写作的场景卡。场景不是镜头列表，而是“目标—冲突—转折—收益/期待”的因果节点。

## 工作步骤

1. 必须读取：
   - `chapter_current_context_pack.json`
   - `chapter_current_plan.json`
2. 输出并保存 `chapter_current_scenes.json`。若能确定章节号，也额外保存 `chapter_XXX_scenes.json`。
3. 保存 JSON 时 `mime_type` 使用 `application/json`。

## 场景设计规则

- 每个场景必须服务本章目标。
- 场景之间必须因果递进，不能只是并列事件。
- 每个场景至少包含一个：压力升级、信息差、人物误判、主角选择、局面反转、收益兑现、钩子推进。
- 场景数量默认 4 个；信息密度高可 3 个，复杂章节可 5 个。
- 每个场景都要有 `must_not_show`，防止提前揭露或写偏。

## JSON Schema

```json
{
  "chapter_no": 1,
  "scene_count": 4,
  "chapter_flow": "",
  "scenes": [
    {
      "scene_no": 1,
      "scene_goal": "",
      "location": "",
      "characters_on_stage": [],
      "conflict": "",
      "opposing_interest": "",
      "turning_point": "",
      "payoff": "",
      "hook_to_next_scene": "",
      "word_budget": 800,
      "must_show": [],
      "must_not_show": []
    }
  ],
  "rhythm_notes": []
}
```

## 质量门槛

- `chapter_flow` 能看出整章节奏曲线。
- `opposing_interest` 不能空泛，必须说明对立方为什么要阻止主角。
- `turning_point` 不能只是“主角成功”，要写清成功方式或局面变化。
- `hook_to_next_scene` 要让下一场有进入理由。
