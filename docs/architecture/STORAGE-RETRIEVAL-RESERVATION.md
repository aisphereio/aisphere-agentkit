# Conversation / File / Memory / Retrieval 预留边界

- 状态：Reserved / Deferred Implementation
- 日期：2026-08-07
- 适用仓库：`aisphere-agentkit`
- 依赖：ADR-001、ADR-002、SYSTEM-BOUNDARIES.md

## 目的

AISphere 当前优先收口 Runtime、Tool、Approval、Credential、Sandbox 和 Hub Definition contracts。

Conversation、File/Knowledge、Memory 与 Retrieval 仍是 Runtime 的核心逻辑能力，但本阶段**只保留领域位置与接口边界，不建设临时持久化实现**。

后续计划使用 OceanBase 方向承载多模数据与统一混合检索。当前不提前绑定具体表结构、索引类型、向量参数、分块策略、Embedding/Rerank 模型或对象存储拓扑，避免先实现 PostgreSQL/MinIO/独立 Vector Store 再迁移产生第二套事实源。

## 领域位置

这些能力属于 AISphere Runtime 的 Context / Knowledge bounded context：

```text
AISphere Runtime
├── Conversation
│   ├── ConversationRepository
│   ├── MessageRepository
│   └── SummaryRepository
├── File / Knowledge
│   ├── FileRepository
│   ├── ContentIndexer
│   ├── FileRetriever
│   └── document/chunk/modality metadata
├── Memory
│   ├── MemoryRepository
│   ├── MemoryDelta
│   ├── MemoryPolicy
│   └── MemoryRetriever
├── Retrieval
│   ├── RetrievalQuery
│   ├── RetrievalFilter
│   ├── HybridRetriever
│   └── RetrievedContext
└── Context Builder
```

Context Builder 只依赖逻辑 ports：

```text
Context Builder
  -> ConversationReader
  -> MemoryRetriever
  -> FileRetriever
  -> RetrievalEngine
  -> ModelContext
```

物理持久化实现通过 adapter 注入：

```text
Runtime domain ports
        |
        v
Storage / Retrieval adapters
        |
        +----> future OceanBase implementation
```

## Conversation 边界

Conversation 表示持续对话容器，Message 表示用户、助手、Tool Observation 等对话内容。

Conversation/Message 与 Runtime execution facts 必须分离：

```text
Conversation / Message = 用户可持续上下文
Run / Snapshot / Attempt / RuntimeEvent = 一次执行事实
```

RuntimeEvent 不能被当作 Conversation Store；Conversation Store 也不能替代 RuntimeEvent Ledger。

## File / Knowledge 边界

File/Knowledge 表示用户或项目可长期复用的内容资产及其可检索表示，例如：

- 原始文件引用；
- 文档结构；
- chunk；
- modality metadata；
- extraction/index state；
- retrieval metadata；
- source provenance。

File/Knowledge 不等于 Runtime Artifact：

```text
File / Knowledge
  长期输入知识、用户/项目资料、可检索内容

Artifact
  Run 执行产生的输出文件、报告、代码、图片等运行产物
```

两者未来可以共享物理存储设施，但领域对象和生命周期保持分离。

## Memory 边界

Memory 是 Runtime Context 的长期、可审查知识，不是 Skill，也不是简单 Session 历史副本。

Memory 至少保留：

- scope：user / project / agent / conversation；
- source provenance；
- lifecycle；
- policy；
- MemoryDelta；
- retrieval metadata。

具体自动提取、合并、遗忘、冲突解决与向量化策略暂缓设计。

## Retrieval 边界

Retrieval 是 Conversation、File/Knowledge、Memory 等 Context sources 的统一查询能力。

目标逻辑协议：

```text
RetrievalQuery
  + scope/filter
  + lexical/vector/multimodal intent
        |
        v
HybridRetriever
        |
        v
RetrievedContext[]
        |
        v
Context Builder token budget / ranking
```

Runtime 不应让 Agent、Skill 或 Tool 直接依赖某个数据库 SDK。所有检索通过 Retrieval port 完成。

## OceanBase 预留

OceanBase 是后续优先评估的统一存储与混合检索实现方向。

本阶段只保留 adapter boundary，不承诺：

- 单库还是多库；
- 原始 blob 是否直接进入 OceanBase；
- 向量索引具体实现；
- 全文/向量/结构化/多模检索的组合方式；
- chunk schema；
- embedding/rerank 模型；
- retrieval fusion 算法；
- 独立 Retrieval Service 是否需要拆出。

这些决策应在 Runtime Tool/Approval/Hub ExecutionSpec/Sandbox contracts 收口后，通过独立 ADR 确认。

## 当前明确不做

- 不为 Conversation 新建临时 PostgreSQL schema；
- 不为 Memory 建临时 pgvector/Vector DB；
- 不为 File Knowledge 新建一套临时 MinIO metadata control plane；
- 不因为缺少最终存储而让 Context Builder 依赖具体数据库；
- 不把 Hub 的 Skill package storage 混入 Runtime Conversation/Memory/Knowledge domain；
- 不把 RuntimeEvent 当作通用消息数据库。

## 实施时机

当前只要求领域位置和接口名称稳定。

建议真正实现顺序：

```text
1. Runtime legacy execution cleanup
2. Tool Compiler / Unified Invocation Pipeline
3. ApprovalGrant / Credential Broker
4. Hub immutable ExecutionSpec contracts
5. SandboxLease / Tool Server contracts
6. Model Gateway integration
7. Context Builder skeleton + storage/retrieval ports
8. OceanBase adapter / indexing / hybrid retrieval
9. Conversation + File/Knowledge + Memory product APIs
```

在第 7 步以前，不应为了展示功能而提前建设临时持久化后端。
