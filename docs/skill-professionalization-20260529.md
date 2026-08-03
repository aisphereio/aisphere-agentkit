# Skill 专业化改造说明（2026-05-29）

## 背景

当前项目已经具备 `skills/` 文件系统仓库和 `skills:` Agent 配置能力，但部分业务方法仍直接写在 `agent.yaml` 的 `instruction` 中，导致：

- Agent 既承担角色定义，又承担专业方法论，提示词膨胀。
- 同一套写作规程难以复用到多个 Agent。
- 后续做 Skill 管理、上传、版本化、灰度时，业务能力不够模块化。
- 章节流水线节点虽然是确定性顺序，但每个节点的质量标准还散落在 Agent 内部。

本次改造把这些内置方法论迁移为独立 `SKILL.md` 包，使 Agent 回到“角色 + 路由 + 工具边界”，Skill 负责“专业规程 + artifact 协议 + 质量门槛”。

## 新增 Skill

| Skill | 作用 | 主要产物 |
| --- | --- | --- |
| `novel-project-initialization` | 从稀疏用户偏好初始化网文项目档案 | `opening_intake_brief.md`、`premise_design.md`、`worldbuilding_design.md`、`story_bible.md`、`character_cards.json`、`style_guide.md`、`reader_payoff_strategy.md`、`forbidden_deviations.md`、`story_state.json` |
| `novel-intake-autofill` | 开题表单 AI 辅助填写 | 表单 `initial_values` JSON |
| `novel-chapter-context-pack` | 章节流水线上下文压缩 | `chapter_current_context_pack.json` |
| `novel-chapter-planning` | 章节目标、冲突、爽点和钩子设计 | `chapter_current_plan.json` |
| `novel-scene-design` | 章节拆成 3-5 个可写场景卡 | `chapter_current_scenes.json` |
| `novel-chapter-drafting` | 根据上下文、计划、场景卡写章节草稿 | `chapter_current_draft.md` |
| `novel-chapter-review` | 严格审稿，输出结构化评分和修订指令 | `chapter_current_review.json` |
| `novel-chapter-revision` | 根据审稿结果定稿 | `chapter_current_final.md` |
| `novel-story-state-update` | 从最终章节更新故事状态和长期线索 | `story_state.json`、`active_threads.json`、`chapter_current_summary.md`、`chapter_current_memory_delta.json` |

## 修改的 Agent

### `novel_step1_intak`

原来大段项目初始化方法写在 `agent.yaml` 中。现在：

- `agent.yaml` 只保留角色、边界、完成后推进方式。
- 专业方法迁移到 `skills/novel-project-initialization/SKILL.md`。
- 保留 `novel-opening-intake`、`novel-premise-design`、`novel-worldbuilding-design` 作为阶段能力补充。

### `novel_intake_autofill`

原来补表单逻辑写在 `autofill_agent.yaml` 中。现在：

- 专业补全规则迁移到 `skills/novel-intake-autofill/SKILL.md`。
- Agent 只声明输出 JSON 对象，不输出解释或 Markdown。

### 章节流水线节点

以下节点都从“内置完整规程”改为“加载对应 Skill”：

- `novel_context_pack_agent` -> `novel-chapter-context-pack`
- `novel_chapter_plan_agent` -> `novel-chapter-planning`
- `novel_scene_design_agent` -> `novel-scene-design`
- `novel_chapter_writer_agent` -> `novel-chapter-drafting`
- `novel_chapter_review_agent` -> `novel-chapter-review`
- `novel_chapter_rewrite_agent` -> `novel-chapter-revision`
- `novel_story_state_update_agent` -> `novel-story-state-update`

## 设计原则

1. **Skill ID 稳定且无业务路径前缀**  
   例如使用 `novel-chapter-review`，不使用 `business-novel-opening-review`。

2. **Agent 不再承载大段专业方法**  
   Agent 只描述角色、边界、工具和必须加载的 Skill。

3. **Skill 明确 artifact 协议**  
   每个 Skill 都写清输入 artifact、输出 artifact、保存文件名、JSON/Markdown 结构和 `mime_type`。

4. **Skill 明确质量门槛**  
   不只告诉模型“做什么”，还告诉模型“怎样算合格”。

5. **平台控制流程，Agent 负责明确产物**  
   章节生产仍由 `SequentialAgent` 固定执行：上下文打包 -> 章节计划 -> 场景卡 -> 草稿 -> 审稿 -> 修订 -> 状态更新。

## 后续建议

- 给 Skill 增加 `references/` 下的 few-shot 示例，例如优秀 `chapter_current_plan.json`、`chapter_current_review.json`。
- 给 JSON 产物增加机器可校验 schema，后续由平台在 `save_artifact` 前后做校验。
- 在前端 Agent 配置页展示 Skill 列表、说明和适用阶段。
- 增加 Skill 包版本字段，后续支持上传、启用、禁用、灰度。
