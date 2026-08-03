# 平台改进提案闭环

这个能力用于把一次真实 Agent 运行中暴露的问题，转成可审核、可回滚、可验证的平台变更。

## 目标

业务 Agent 可以发现问题，但不能直接修改自己。平台通过三段角色完成闭环：

1. `objective_review_agent`：基于证据客观审查，提出 `improvement_issue`。
2. `self_improvement_agent`：把确认过的问题转成 `improvement_proposal` 和 `changes`。
3. `approval_packet_agent`：生成给人类审核的审批包。

人类先确认问题值得改，再审批具体 diff。批准后才允许执行补丁或登记应用结果。

## 数据模型

`improvement_issues` 记录问题：

- `run_id` / `session_id` / `app_name` / `agent_name`
- `issue_type`：`workflow_gap`、`skill_gap`、`tool_gap`、`approval_gap` 等
- `severity`
- `evidence_json`
- `status`：`open`、`proposed`、`dismissed`、`resolved`

`improvement_proposals` 记录提案：

- `source_issue_id`
- `proposal_type`：`update_agent`、`update_workflow`、`update_skill`、`create_skill`、`update_tool`、`update_docs`
- `target_refs_json`
- `risk_level`
- `status`：`draft`、`pending_review`、`approved`、`rejected`、`applied`、`failed`

`improvement_changes` 记录具体变更：

- `change_type`：`file_patch`、`yaml_patch`、`skill_patch`、`tool_schema_patch`、`agent_binding_patch`
- `target_path`
- `diff_text`
- `patch_text`
- `status`

## API

问题：

- `GET /platform/improvement-issues`
- `POST /platform/improvement-issues`
- `GET /platform/improvement-issues/{issue_id}`
- `PATCH /platform/improvement-issues/{issue_id}`

提案：

- `GET /platform/improvement-proposals`
- `POST /platform/improvement-proposals`
- `GET /platform/improvement-proposals/{proposal_id}`
- `POST /platform/improvement-proposals/{proposal_id}/approve`
- `POST /platform/improvement-proposals/{proposal_id}/reject`
- `POST /platform/improvement-proposals/{proposal_id}/mark-applied`

`mark-applied` 只登记执行结果，不绕过审批自动写文件。

## 自然语言操作路径

用户可以说：

- “刚才这个 Agent 哪里做得不好，帮我复盘。”
- “把这个 Skill 的输出协议改稳定。”
- “给这个流程加一个记忆 Agent。”
- “这个工具参数太宽了，帮我设计更安全的 schema。”
- “创建一个专门审查爽点节奏的子 Agent。”

平台应把自然语言变成：

```text
用户反馈
  -> objective_review_agent 生成问题
  -> 人类确认问题
  -> self_improvement_agent 生成提案和 diff
  -> approval_packet_agent 生成审批包
  -> 人类批准或拒绝
  -> 平台执行服务应用或登记应用结果
```

## 审批边界

以下动作必须审批：

- 修改 Agent YAML 或工作流顺序
- 新增/删除/绑定 Tool
- 新增 Skill 或修改 Skill 输出协议
- 新增记忆 Agent 或长期状态写入
- 改动会影响多个 App 或线上运行

以下动作不得由业务 Agent 直接做：

- 直接写 `agents/**/*.yaml`
- 直接写 `skills/**/SKILL.md`
- 直接开启高风险工具权限
- 直接修改数据库 schema
- 直接删除已有工作流节点

## Book Dissector Skill Evaluation Proposal Types

章节拆书的 Skill 评估闭环可以写入平台改进提案，但不能绕过人工审批。

### Issue Types

- `chapter_skill_gap`：章节技法包缺失、过泛、不可执行或过拟合原书桥段。
- `chapter_brief_gap`：压缩 brief 没有交代清楚目标、冲突、人物状态或结尾钩子。
- `chapter_context_gap`：缺少必要前情、人物关系、世界规则或资源状态。
- `chapter_style_gap`：缺少视角、句式节奏、对白密度、情绪纹理等风格约束。
- `reconstruction_execution_gap`：隔离复写 Agent 没有执行已提供的规则。

### Proposal Types

- `update_chapter_skill_pack`：更新某个 `chapter_skill_pack` 的 technique、style_fingerprint 或 context_pack。
- `promote_chapter_skill_pack_to_skill`：把多章验证过的技法包提升为正式 Skill 草案。
- `update_reconstruction_brief_schema`：调整 compressed_brief 或 scene_contract 的字段要求。
- `add_minimal_context_pack`：为盲测补充最小必要上下文，但不泄露原文长内容。
- `retry_reconstruction_probe`：在 Skill Pack 足够但执行偏差时重跑隔离写作测试。

### Approval Rule

The platform must not automatically publish a new skill from reconstruction results. It may draft an improvement proposal, but promotion to a reusable skill requires explicit human approval.
