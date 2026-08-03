# Book Dissector Skill-Eval E2E

## 目标

验证“拆书 → 章节技法包 → 隔离复写盲测 → Gap Report → 多章技法归纳 → Skill 迭代建议”的第一版闭环是否可跑。

这个流程的核心不是让模型直接从一章生成“完美 Skill”，而是通过盲测和差距归因，把可迁移技法逐步提纯。

## 前置条件

- Go 后端已启动。
- Angular Web 前端已启动。
- 当前 app 选择 `book_dissector`。
- 平台上传中心已有 TXT/Markdown 小说文件。
- `book_dissector` 已加载这些 Skill：
  - `novel-book-dissect-core`
  - `novel-chapter-function-analysis`
  - `novel-writing-skill-extraction`
  - `novel-chapter-skill-pack`
  - `novel-chapter-reconstruction-eval`
  - `novel-cross-chapter-skill-distillation`

## 场景 A：单章 Skill Pack + 盲测

1. 在 `book_dissector` 中发送上传文件引用或让 Agent 查询上传中心。
2. 确认 Agent 使用 `upload_get` / `upload_attach_artifact` / `book_split_from_artifact`。
3. 确认 Agent 返回：`book_id`、章节数、前 20 章标题、warnings。
4. 发送：

   ```text
   用第一章提炼出的 skill pack 做一次盲测复原，只给子 agent brief 和 skill_pack，不给原文。
   ```

5. 确认创建以下产物：

   ```text
   chapter_analysis_<book_id>_1.md
   chapter_skill_pack_<book_id>_1.json
   reconstruction_probe_<book_id>_1_1.md
   reconstruction_gap_report_<book_id>_1_1.json
   ```

6. 确认 UI 显示 `book_dissector -> book_chapter_reconstruction_probe`，而不是把子 Agent 当普通工具展示。
7. 确认 artifact 列表只显示名称和 badge，不在列表渲染时加载内容；只有点击打开、预览、下载时才加载内容。
8. 确认最终回答包含明确决策：

   - `accept_skill_pack`
   - `refine_brief`
   - `refine_skill_pack`
   - `add_context_pack`
   - `retry_probe`
   - `request_human_review`

## 场景 B：多章归纳 Skill Candidate

在至少两个章节完成 `chapter_skill_pack` 后，发送：

```text
基于第 1 到第 3 章的 chapter_skill_pack 和 gap_report，归纳 cross_chapter_skill_candidates，不要直接发布正式 skill。
```

确认创建：

```text
cross_chapter_skill_candidates_<book_id>_1_3.json
```

该产物必须区分：

- `stable_techniques`：至少两个样本中成立，可进入人工审核。
- `candidate_techniques`：单章成立或证据不足，需要更多样本。
- `rejected_overfit_details`：原书专名、具体桥段、特定道具、过小场景。
- `upgrade_recommendations`：下一步盲测或人工审核建议。

## 机器校验

`save_artifact` 会对以下命名的 JSON 产物做结构校验：

```text
chapter_skill_pack_*.json
reconstruction_gap_report_*.json
cross_chapter_skill_candidates_*.json
```

校验失败时，工具会返回类似错误：

```text
artifact "chapter_skill_pack_x_1.json" failed chapter_skill_pack validation: techniques must contain at least 3 items; style_fingerprint.pov must be a non-empty string
```

Agent 不应该换个名字绕过校验，而应该补齐字段后重试。

## 通过标准

- 原文只被 `book_dissector` 用于分析和抽象；复写子 Agent 没有接触原文。
- `chapter_skill_pack` 的 technique 是通用动作，不绑定原书角色和桥段。
- `reconstruction_gap_report` 能解释失败来源，而不是只说“写得不够好”。
- `cross_chapter_skill_candidates` 不把单章私货直接提升为通用 Skill。
- 改进建议只生成提案或待审核草案，不自动发布 Skill。

## 建议测试用语

```text
先处理我上传的这本书，完成切章，并展示前 20 章标题。
```

```text
用第 1 章做一次 skill 教研闭环：拆解章节、生成 chapter_skill_pack、让隔离子 Agent 盲测复写、再生成 gap_report。
```

```text
继续用第 2 章和第 3 章也做同样的 skill_pack 和盲测。
```

```text
基于第 1 到第 3 章的 skill_pack 和 gap_report，归纳 cross_chapter_skill_candidates，稳定技法进入人工审核，不要自动发布正式 Skill。
```

## 常见问题：停在“调用章节复写盲测智能体”后没有继续

如果事件流停在 `book_chapter_reconstruction_probe` 工具调用之后，通常不是模型在思考，而是 AgentTool 被配置成了 `skip_summarization: true`。该配置会让父 Agent 在子 Agent 返回后直接结束本轮，导致后续的 `reconstruction_probe_*.md` 保存、`reconstruction_gap_report_*.json` 保存和最终决策都不会继续执行。

拆书教研闭环需要父 Agent 在子 Agent 返回后继续汇总，因此 `book_dissector/root_agent.yaml` 中的配置应为：

```yaml
- name: AgentTool
  args:
    agent:
      config_path: ./chapter_reconstruction_probe_agent.yaml
      skip_summarization: false
```

判断是否跑完整闭环，不要只看是否出现了“章节复写盲测智能体”工具调用，而要看产物目录中是否同时出现：

- `reconstruction_probe_<book_id>_<chapter_index>_<attempt>.md`
- `reconstruction_gap_report_<book_id>_<chapter_index>_<attempt>.json`

只有这两个产物保存成功后，单章 Skill 教研闭环才算完成。
