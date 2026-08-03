# Agent 自提升闭环设计

本文定义平台层的“运行现场问题发现 → 客观审查 → 改进提案 → 自提升补丁草案 → 人类审批”的工程闭环。

## 目标

在真实 Agent 工作过程中发现的问题，往往比脱离现场的设计更具体。平台应该允许用户基于一次 run、一个 artifact、一次 tool error 或一次不满意反馈，生成可审核的改进提案。

这个能力不是让业务 Agent 自动修改自己，而是把问题变成可审查、可回滚、可验证的变更。

## 核心原则

1. 业务 Agent 可以暴露问题，但不能直接自改。
2. 客观审查角色负责替代人类提出质疑，不负责生成 patch。
3. 自提升角色负责生成改进提案和 patch draft，不直接 apply。
4. 审批包角色负责把提案整理给人类审核，不替用户批准。
5. 所有变更必须带证据、diff、影响范围、风险和回滚方案。
6. 平台应用变更前必须经过人类审批。

## 角色模型

| 角色 | role | 职责 |
|---|---|---|
| 业务 Agent | worker / reviewer / workflow / entry_router | 完成业务任务，必要时暴露问题 |
| 客观审查 Agent | objective_reviewer | 基于证据指出哪里确实有问题 |
| 自提升 Agent | self_improver | 生成可审核的修改提案和 patch draft |
| 审批包 Agent | approval_packet_builder | 整理证据、diff、风险、验证和回滚 |
| 人类用户 | human_reviewer | 最终批准、拒绝或要求修改 |
| 平台 Apply Service | change_applier | 批准后应用变更并记录版本 |

## 内置 Agent

本版本新增 `agents/agent_ops`：

```text
agent_ops                       governance_router
├── objective_review_agent       objective_reviewer
├── self_improvement_agent       self_improver
└── approval_packet_agent        approval_packet_builder
```

### agent_ops

平台治理入口，负责组织流程，不直接修改文件。

### objective_review_agent

基于证据产出 `agent_improvement_issue.md`，指出：

- 确定问题
- 疑似问题
- 缺少证据
- 影响范围
- 是否值得生成改进提案

### self_improvement_agent

基于已确认 issue 产出 `agent_improvement_proposal.md`，包含：

- 修改目标
- 修改文件
- diff 草案
- 风险
- 验证
- 回滚

### approval_packet_agent

产出 `agent_improvement_approval_packet.md`，面向人类审核。

## 证据来源

优先使用：

1. 用户反馈。
2. runtime trace：`agent.enter`、`agent.exit`、`agent.transfer`、`tools.bound`、`tool.call`、`tool.result`、`agent.error`。
3. PG run_steps：平台级运行摘要。
4. artifact：输入、输出、中间产物。
5. Agent YAML：职责、sub_agents、tools、skills。
6. Skill：注入规则、输出协议、质量标准。
7. Tool 事件：绑定、调用、失败、审批。

## 推荐交互流程

### 1. 用户指出问题

示例：

```text
刚才 chapter_review_agent 审查太空泛了，帮我看看是不是 skill 有问题。
```

### 2. agent_ops 组织客观审查

生成：

```text
agent_improvement_issue.md
```

### 3. 用户确认需要改

用户可以说：

```text
这个问题确认，生成一个修改提案。
```

### 4. self_improvement_agent 生成提案

生成：

```text
agent_improvement_proposal.md
```

### 5. approval_packet_agent 生成审批包

生成：

```text
agent_improvement_approval_packet.md
```

### 6. 人类审批

人类审核：

- 证据是否充分
- diff 是否合理
- 风险是否可接受
- 验证方案是否明确
- 是否需要缩小修改范围

### 7. 应用变更

第一版不自动 apply。用户可以手动复制 patch 或让后续平台 Apply Service 执行。

## 后续平台表设计

后续可新增：

```text
improvement_issues
improvement_proposals
improvement_changes
improvement_reviews
improvement_apply_results
```

第一版先用 artifact 保存审查、提案和审批包。

## 与可观测性的关系

这个闭环依赖 P2.1 Agent Runtime Observability：

```text
先看见发生了什么
再客观判断哪里不好
再生成可审核修改
最后由人类批准
```

没有 trace 和 run_steps，自提升就容易变成拍脑袋改 prompt。

## 禁止事项

- 业务 Agent 不允许直接修改自身 YAML 或 Skill。
- 自提升 Agent 不允许声称已经应用修改。
- 证据不足时不允许生成大范围 patch。
- 不允许绕过人类审批。
- 不允许隐藏风险和回滚成本。

## Book Skill Evaluation Loop Ownership

第一版章节技法评估闭环放在 `book_dissector` 内部，因为它依赖小说上传、确定性切章、章节读取、相邻上下文、章节功能分析和网文技法抽象。平台层不要直接替代这个领域拆解动作。

闭环必须产出平台中立 artifact：

- `chapter_analysis`
- `chapter_skill_pack`
- `reconstruction_probe`
- `reconstruction_gap_report`
- `skill_improvement_proposal`

只有 `chapter_analysis` 和 `chapter_skill_pack` 可以由原章节文本推导。`reconstruction_probe` 子 Agent 只能接收 `compressed_brief`、`chapter_skill_pack`、`style_fingerprint` 和可选的极小 `context_pack`，不得读取原章全文、manifest 全量内容或长篇章节分析。

平台负责持久化捕获、提案审查、人工批准、Skill 版本化和回滚；`book_dissector` 负责领域提炼、盲测编排和 Gap 判断。

章节技法评估闭环的问题类型包括：

- `chapter_skill_gap`：技法包过泛、不可执行或过拟合原书。
- `chapter_brief_gap`：压缩 brief 缺少目标、冲突、人物状态或钩子。
- `chapter_context_gap`：缺少必要前情、人物关系、世界规则或资源状态。
- `chapter_style_gap`：风格指纹不足，导致样稿不像同类文本。
- `reconstruction_execution_gap`：隔离写作 Agent 没有遵守已提供的技法包。

平台不得根据盲测结果自动发布新 Skill。它可以生成 `skill_improvement_proposal`，但把 `chapter_skill_pack` 提升为可复用 Skill 必须经过明确的人类审批。
