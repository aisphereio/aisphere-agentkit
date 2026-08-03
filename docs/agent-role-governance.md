# Agent 角色治理规范

为了让平台理解每个 Agent 在系统中的职责，本版本开始在 Agent YAML 中引入 `metadata.role`。

## 为什么需要 role

多 Agent 系统里，`agent_class` 只能说明运行机制，例如 `LlmAgent`、`SequentialAgent`、`ParallelAgent`。它不能说明业务角色。

例如两个 `LlmAgent` 可能分别是：

- 入口路由器
- 章节写手
- 审稿员
- 自提升提案生成器

所以需要 `metadata.role` 辅助平台做图谱、可观测性、审查和改进。

## 推荐 role

| role | 含义 |
|---|---|
| entry_router | 用户入口，负责理解意图和路由 |
| workflow_router | 某个业务域内的路由 Agent |
| workflow | 确定性流程，如 Sequential / Parallel / Loop |
| worker | 具体工作 Agent，只做一个明确任务 |
| objective_reviewer | 客观审查 Agent，指出问题 |
| self_improver | 自提升 Agent，生成提案和 patch draft |
| approval_packet_builder | 审批包 Agent，整理人类审核材料 |
| ops_agent | 运维环境管理 Agent |
| test_agent | 平台测试 Agent |
| governance_router | 平台治理入口 |

## YAML 示例

```yaml
name: novel_chapter_review_agent
model: default
agent_class: LlmAgent
description: 检查章节草稿是否完成目标、爽点、推进、一致性和章尾钩子。
metadata:
  role: objective_reviewer
  stage: chapter_review
```

## role 和 agent_class 的区别

| 字段 | 说明 |
|---|---|
| agent_class | 怎么运行：LlmAgent / SequentialAgent / ParallelAgent |
| metadata.role | 在平台里扮演什么角色 |

## role 对平台的价值

后续平台可以基于 role 做：

- Agent Graph 展示。
- 运行 trace 聚合。
- 审查哪些 Agent 可以给出客观建议。
- 限制 self_improver 不能直接 apply。
- 识别 workflow 是否缺少 reducer。
- 判断业务 Agent 是否错误承担平台治理职责。

## 当前落地

本版本已经给现有 Agent 增加了基础 `metadata.role`，并新增了 `agent_ops` 治理 Agent 组。
