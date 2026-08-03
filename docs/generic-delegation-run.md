# 通用委托任务运行器设计

## 目标

主 Agent 像领导一样做规划，不亲自读取大量正文，不把长任务塞进一个 session。专业工作由子 Agent 独立 session 执行。

## 核心组件

- `DelegationRunToolset`
  - `delegation_run_start`
  - `delegation_run_get`
- `task_manager`
  - 通用管理 Agent，负责理解目标、选择 worker、创建 run。
- `chapter_analysis_worker_mcp`
  - 通用章节 worker，不固定对话；根据 prompt 的 analysis_type/focus 做专业分析。
- `skill_distiller_mcp`
  - 通用汇总 Agent，只读取小 JSON 结果，不读取章节正文。

## 为什么替代 AnalysisRunToolset

旧的 `AnalysisRunToolset` 写死了：

- worker: `chapter_dialogue_worker_mcp`
- final: `dialogue_skill_distiller_mcp`
- prompt: 对话分析
- skill_type: `dialogue_chapter_analysis`

这会把平台做成固定流程。新的 `DelegationRunToolset` 不知道小说、对话、章节，只知道如何启动多个独立 Agent session。

## 示例：分析《大清首富》前 30 章对话

Manager 调用：

```json
{
  "name": "delegation_run_start",
  "arguments": {
    "run_type": "novel_chapter_skill_extraction",
    "objective": "分析《大清首富》前30章，逐章提炼对话写作技法，并汇总成通用对话写作Skill",
    "default_agent_app": "chapter_analysis_worker_mcp",
    "concurrency": 8,
    "shared_state": {
      "book_id": "b_xxx",
      "book_name": "大清首富"
    },
    "range": {
      "from": 1,
      "to": 30,
      "task_id_template": "chapter_{{item04}}",
      "prompt_template": "任务：分析《大清首富》第{{item04}}章的对话写法。book_id=b_xxx, chapter_no={{item04}}, analysis_type=dialogue, skill_type=dialogue_chapter_analysis。请调用get_novel_chapter读取本章，提炼可复用对话技法，调用save_skill_batch保存，batch_no={{item}}，overwrite=true。不要输出章节正文。"
    },
    "final": {
      "agent_app": "skill_distiller_mcp",
      "prompt": "读取book_id=b_xxx的dialogue_chapter_analysis batch 1-30，归纳成通用对话写作Skill，保存为dialogue_final_skill。不要读取章节正文。"
    }
  }
}
```

主 session 只得到 run_id 和进度，不会得到 30 章正文。

## 查询进度

```json
{
  "name": "delegation_run_get",
  "arguments": {"run_id": "delegate_xxx"}
}
```

## 设计原则

1. Manager 不干活，只规划和委托。
2. Worker 自己带 skill/tool 干专业活。
3. 每个 worker 是独立 session。
4. 正文只进入 worker session。
5. 结果必须保存成 artifact/MinIO 对象。
6. 主 session 只保存 run_id、进度、产物指针。
7. 不要把某个业务流程写进运行器。
