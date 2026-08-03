# Project Workspace / Agent Sandbox 设计说明

## 1. 背景

当前平台已经具备 Project、Agent、Tool、Skill、Upload、Artifact、NovelStore、ObjectStore 等基础能力。

在继续推进“小说拆书 → Skill 提炼”以及后续代码类、运维类、数据类项目时，需要明确一个关键原则：

**Project 的工作现场优先使用文件系统 Workspace，而不是直接把所有中间过程都放到 S3 / MinIO。**

S3 / MinIO 适合做长期对象存储、归档、版本化产物和跨环境分发；但 Agent 的实际执行现场更适合是一个可管理的沙箱目录。

这个沙箱可以理解为：

```text
Project Workspace / Agent Sandbox
```

它里面可以有文件、脚本、二进制命令、项目配置、运行日志、临时产物、测试结果等。

---

## 2. 核心原则

### 2.1 Workspace 是 Agent 的工作现场

Workspace 应该是文件系统优先。

典型路径可以是：

```text
/workspaces/{tenant_id}/{project_id}/
```

或者按任务细分：

```text
/workspaces/{tenant_id}/{project_id}/runs/{run_id}/
```

其中可以包含：

```text
source/
normalized/
chapters/
outputs/
artifacts/
logs/
tools/
bin/
tmp/
state/
```

Agent 在这里可以执行受控命令、读取文件、生成中间结果、保存日志。

---

### 2.2 S3 / MinIO 是长期资产库，不是主工作目录

MinIO / S3 适合保存：

```text
原始上传文件归档
确认后的小说正文
确认后的 active split
最终 Skill 包
可下载交付物
长期报告
版本化产物
```

但不适合承载所有临时处理过程，例如：

```text
切书临时文件
失败的中间文件
脚本运行缓存
LLM 临时输出
多次尝试的中间结果
```

这些应该先留在 Workspace 文件系统里，确认后再归档到对象存储。

---

### 2.3 一个项目可以绑定一种项目类型

Project 不只是一个普通分组，还可以有类型。

例如：

```text
novel          小说拆书 / 写作 / Skill 提炼项目
code           代码开发 / 修复 / 测试项目
ops            运维诊断 / 日志分析 / 环境管理项目
data           数据处理 / 表格 / 报表项目
research       文档研读 / 资料分析项目
```

不同项目类型对应不同的默认沙箱环境。

---

## 3. 项目类型与沙箱能力

### 3.1 小说类项目：novel

小说类项目的 Workspace 可以内置：

```text
bin/book-splitter
bin/text-normalizer
bin/chapter-inspector
bin/skill-pack-validator
```

目录结构示例：

```text
/workspaces/{tenant_id}/{project_id}/
  source/
    raw.txt
  normalized/
    book.utf8.txt
  splits/
    split-001/
      manifest.json
      chapters/
        000001.txt
        000002.txt
  analysis/
    dialogue/
    pacing/
    character/
  skill/
    drafts/
    versions/
  logs/
    ingest.log
    split.log
    skill-run.log
```

确定性动作可以由本地命令完成：

```bash
book-splitter --input normalized/book.utf8.txt --output splits/split-001
chapter-inspector --manifest splits/split-001/manifest.json
skill-pack-validator --input skill/drafts/novel-dialogue-writing.md
```

Agent 不需要自己“想办法切书”，而是调用受控 Tool，Tool 再调用沙箱里的确定性命令。

---

### 3.2 代码类项目：code

代码类项目的 Workspace 可以内置：

```text
git
go
node
python
pytest
npm
pnpm
docker client
static analyzer
formatter
```

目录结构示例：

```text
/workspaces/{tenant_id}/{project_id}/
  repo/
  patches/
  test-results/
  logs/
  tmp/
```

Agent 可以执行：

```bash
go test ./...
npm run build
pytest
git diff
```

但执行策略需要受控：

```text
只读命令可以直接执行
写入命令需要确认
危险命令必须审批
```

---

### 3.3 运维类项目：ops

运维类项目的 Workspace 可以内置：

```text
kubectl
helm
docker
jq
yq
ssh client
log parser
```

目录结构示例：

```text
/workspaces/{tenant_id}/{project_id}/
  env/
    kubeconfigs/
    inventories/
  logs/
  reports/
  scripts/
```

Agent 可以读取日志、运行诊断命令、生成报告。

---

## 4. Workspace 与 ObjectStore 的关系

推荐关系：

```text
Workspace 文件系统：
  Agent 的执行现场
  临时文件
  中间文件
  可调试目录
  工具命令执行环境

ObjectStore / MinIO：
  长期资产
  已确认产物
  可下载交付件
  跨环境持久化对象
```

推荐流程：

```text
用户上传文件
  ↓
保存到 Workspace/source
  ↓
Tool 执行格式化 / 检查 / 切分 / 构建
  ↓
Workspace 生成中间结果
  ↓
用户或系统确认结果有效
  ↓
关键结果归档到 ObjectStore
  ↓
数据库记录 asset/state/manifest
```

---

## 5. 小说拆书场景的推荐流程

### 5.1 唯一入口

用户只看到一个入口：

```text
拆书提炼 Skill
```

内部由具体 Agent 编排：

```text
book_skill_runner / novel_book_to_skill
```

不要暴露多个入口：

```text
book_dissector
novel_store_manager
book_preprocessor
```

这些可以作为内部能力存在，但不直接给用户选择。

---

### 5.2 状态检查

每次开始拆书前，先检查项目状态：

```text
novel_project_state(project_id)
```

如果已经存在：

```text
state = split_confirmed
```

则说明这个项目已经完成小说预处理。

此时：

```text
禁止重复上传
禁止重复 UTF-8 转换
禁止重复切分
直接复用 book_id / active_split_id
```

如果用户确实要重做，必须走显式版本化流程：

```text
创建新版本
废弃旧 active split
重新确认 active split
```

不能静默覆盖。

---

### 5.3 UTF-8 转换与切分

UTF-8 转换和切分属于确定性工程动作，优先由 Tool + Workspace 命令完成：

```text
source/raw.txt
  ↓ text-normalizer
normalized/book.utf8.txt
  ↓ book-splitter
splits/split-001/chapters/*.txt
```

模型只在异常场景参与，例如：

```text
简介被切成第一章
目录被当正文
章节标题规则不稳定
章节数量明显异常
某些章节过短/过长
```

模型参与方式是：

```text
读取有限样本 + warnings + 字数分布
  ↓
给出修复建议
  ↓
Tool 使用建议重新切分
  ↓
用户确认
```

模型不能直接写入 active split。

---

## 6. 状态管理建议

状态应该记录在数据库中，而不是只依赖聊天上下文。

小说项目可维护：

```text
project_id
book_id
source_version
normalized_path
active_split_id
chapter_count
status
locked
created_at
updated_at
```

状态枚举：

```text
empty
source_uploaded
normalized
split_candidate_ready
split_confirmed
skill_extracting
skill_draft_ready
completed
```

核心规则：

```text
同一个 project 默认只能有一个 active book
同一个 book 默认只能有一个 active split
所有 Skill 提炼任务必须绑定 active_split_id
一旦 split_confirmed，普通用户不能重复切分
```

---

## 7. Tool / Skill / Agent 分工

### 7.1 Tool

Tool 负责确定性动作：

```text
上传文件落 workspace
UTF-8 转换
切章
保存 manifest
读取章节
写入状态
执行本地二进制命令
归档到 ObjectStore
```

### 7.2 Skill

Skill 负责方法论：

```text
如何分析对话
如何提炼爽点
如何总结人物塑造方法
如何把章节观察沉淀成可复用 Skill
```

Skill 不负责文件存储和状态管理。

### 7.3 Agent

Agent 负责编排：

```text
识别用户意图
检查状态
调用 Tool
必要时请求用户确认
必要时调用模型分析
循环推进 Skill 提炼
保存最终草稿
```

---

## 8. 沙箱环境设计建议

后续可以引入 Sandbox Profile：

```yaml
sandbox_profiles:
  novel:
    image: agent-sandbox-novel:latest
    workspace_root: /workspaces
    tools:
      - book-splitter
      - text-normalizer
      - chapter-inspector
      - skill-pack-validator

  code-go:
    image: agent-sandbox-go:latest
    workspace_root: /workspaces
    tools:
      - go
      - git
      - staticcheck
      - golangci-lint

  ops-k8s:
    image: agent-sandbox-ops:latest
    workspace_root: /workspaces
    tools:
      - kubectl
      - helm
      - jq
      - yq
```

Project 创建时可以绑定：

```text
project_type = novel
sandbox_profile = novel
```

这样 Agent 进入项目时，天然知道当前沙箱有哪些能力。

---

## 9. 未来扩展方向

后续可以逐步实现：

```text
WorkspaceService
SandboxService
CommandToolset
WorkspaceFileToolset
ProjectTypeProfile
CapabilityState
ObjectStoreArchive
```

但当前阶段先不要急着实现。

当前只需要明确方向：

```text
Workspace 优先文件系统
ObjectStore 只做长期归档
不同 Project 类型绑定不同沙箱能力
具体业务能力挂到具体 Agent / Toolset
通用平台只提供扩展机制，不内置小说业务
```

---

## 10. 当前阶段决策

本阶段只记录设计，不实现。

明确以下决策：

1. 不把小说拆书固定做成通用 Project 管理能力。
2. 不把 Workspace 的主要工作流放到 S3 / MinIO。
3. Project Workspace 优先使用文件系统沙箱。
4. MinIO / S3 只保存确认后的长期资产。
5. 小说类项目可以拥有专用沙箱工具，例如 book-splitter。
6. 代码类项目可以拥有专用构建/测试工具。
7. Agent 通过 Toolset 调用沙箱能力，而不是直接自由操作文件系统。
8. 状态管理必须保证同一项目的 active source / active split 唯一。
9. 用户只看到一个业务入口，内部能力可以拆分。
10. 通用平台只承载扩展机制，不污染为某一个业务写死入口。

---

## 11. 一句话总结

**Project Workspace 是 Agent 的沙箱工作现场；ObjectStore 是长期资产库；不同项目类型通过不同 Sandbox Profile 获得专用工具；业务能力通过 Agent + Toolset + Skill 组合实现，但对用户只暴露一个清晰入口。**
