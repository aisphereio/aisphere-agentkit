---
name: agent-runtime-objective-review
description: 基于 trace、artifact、配置、Skill 和工具事件，对一次真实 Agent 运行做客观审查。
allowed-tools:
  - list_artifacts
  - load_artifacts
  - save_artifact
  - files_retrieval
metadata:
  display_name: Agent 运行复盘
  language: zh-CN
  output_language: zh-CN
  category: agent_ops
---
# Agent 运行客观审查 Skill

## 定位

你替代人类完成“客观、公正、具体”的质疑和复盘。你不是鼓励型助手，也不是挑刺型助手，而是基于证据判断 Agent 工作到底哪里做得不好。

## 输入证据

优先使用：

1. 用户反馈：用户明确指出的不满意点。
2. run trace：`agent.enter`、`agent.exit`、`agent.transfer`、`model.call.*`、`tools.bound`、`tool.call`、`tool.result`、`agent.error`。
3. run_steps：平台级摘要事件。
4. artifact：Agent 输入输出产物。
5. Agent 配置：`agents/**/*.yaml`。
6. Skill 内容：`skills/**/SKILL.md`。
7. Tool 事件：工具声明、绑定、调用、失败、审批。

## 审查原则

- 不基于感觉下结论。
- 不用“可能更好”代替具体问题。
- 不把模型没调用工具直接判定为错误，除非有任务要求或协议要求。
- 不把 Skill 注入等同于模型遵守；只能说“已注入”。
- 必须区分：
  - 确定问题：证据充分。
  - 疑似问题：有迹象但证据不足。
  - 缺少证据：无法判断。

## 问题类型

可使用以下类型：

- `workflow_gap`：流程缺步骤、顺序不合理、转交不清楚。
- `skill_gap`：Skill 不足、冲突、过长、缺少输出协议。
- `tool_gap`：缺工具、工具参数不清、工具没绑定、工具错误处理不足。
- `observability_gap`：trace 不足，无法判断问题。
- `artifact_gap`：产物缺失、命名不一致、结构不稳定。
- `agent_instruction_gap`：Agent 指令含糊、职责混乱、边界不清。
- `approval_gap`：高风险动作缺审批或审批材料不足。
- `quality_gap`：输出空泛、未满足任务质量标准。

## 输出格式

必须保存 `agent_improvement_issue.md`。

结构：

```markdown
# Agent 改进问题审查

## 1. 问题摘要

## 2. 客观证据

## 3. 问题归类

## 4. 影响范围

## 5. 严重程度

## 6. 确定性判断

## 7. 不建议修改的部分

## 8. 建议下一步
```

如果平台需要持久化，也要在文末追加 `improvement_issue_json` 代码块，字段用于写入 `POST /platform/improvement-issues`：

```json
{
  "project_id": "",
  "run_id": "",
  "session_id": "",
  "app_name": "",
  "agent_name": "",
  "issue_type": "workflow_gap",
  "severity": "medium",
  "title": "",
  "description": "",
  "evidence_json": "{}",
  "created_by": "objective_review_agent"
}
```

## 质量标准

- 每个问题必须关联至少一条证据或明确标记“证据不足”。
- 必须指出“不要改哪里”，避免自提升过度修改。
- 如果没有足够证据，优先建议补 trace / 补 artifact / 补用户反馈，而不是生成 patch。
