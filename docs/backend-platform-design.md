# 后台平台能力设计（Backend Platform Design）

> 目标：把当前 ADK Go 项目从“本地文件 + YAML 配置 + 单进程调试”推进为可持续扩展的后台平台。本文档先定义能力边界、模块职责和数据归属，后续开发按 `backend-platform-implementation-plan.md` 分阶段推进。

## 1. 当前代码现状

当前项目已经具备一批可承接平台化改造的基础能力：

| 能力 | 已有代码/文档 | 当前状态 | 平台化缺口 |
| --- | --- | --- | --- |
| Runtime Config | `internal/runtimeconfig/config.go`、`docs/runtime-config.md` | 已支持 `adk.yaml`、环境变量、模型别名、filesystem/inmemory storage | 缺少正式生产配置范式、DB/Object/KV 后端接入路线 |
| REST API | `server/adkrest` | 已有 sessions/runtime/apps/metadata/skills/debug/artifacts/traces 等路由 | 缺少 auth、tenant、run、approval、environment、model 管理路由 |
| Session | `session/*`、`session/database/*` | 已有 filesystem/inmemory/database service；database service 基于 GORM | runtime factory 已暴露 sqlite/database session；postgres/mysql driver 下一步接入 |
| Artifact | `artifact/*` | 已有 filesystem/inmemory/GCS | 需要统一 MinIO/S3 生产存储策略和 artifact metadata 索引 |
| Memory | `memory/*` | 已有 filesystem/inmemory 关键词检索 | 缺少长期记忆条目、编辑、归档、向量检索和权限边界 |
| Skill | `internal/skillservice`、`skills/*`、`docs/skill-professionalization-20260529.md` | 已有 filesystem skill 仓库和 `/skills` API | 缺少 DB+对象存储版本化、上传、启停、灰度、资源包管理 |
| Model | `adk.yaml`、`internal/runtimeconfig`、`server/adkrest/controllers/metadata.go` | 已能用 model alias/spec 解析 OpenAI-compatible/Gemini | 缺少后台模型供应商、模型规格、密钥引用、连通性测试和默认策略 |
| Environment | `tool/envmanagertool/*`、`docs/env-management-design.md` | 已有受控环境 Toolset、风险等级、自由度、安全模式 | 环境资产、Secret、操作目录、审批、审计尚未服务化 |
| Resumable Run | `server/adkrest/internal/resumable` | 已有 Redis/standalone run store 思路 | 需要 PG run 事实表 + Redis 运行态配合 |

## 2. 总体架构

第一阶段不拆微服务，仍放在同一个 Go 后端进程里，但需要明确分层：

```text
┌──────────────────────────────────────────────────────────┐
│                    WebUI / CLI / API Client              │
└──────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────┐
│ server/adkrest                                           │
│  - HTTP routing / SSE / CORS / auth middleware           │
│  - platform management APIs                              │
│  - ADK runtime APIs                                      │
└──────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────┐
│ internal/platform                                        │
│  auth / users / runs / approvals / models / skills       │
│  environments / secrets / audit / object / migrations    │
└──────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────┐
│ ADK Core Services                                        │
│ session.Service / artifact.Service / memory.Service      │
│ agent.Loader / runner.Runner / skillservice.Service      │
│ envmanagertool.EnvToolset                                │
└──────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────┐
│ Middleware                                               │
│ PostgreSQL/MySQL/SQLite + Redis + MinIO/S3               │
└──────────────────────────────────────────────────────────┘
```

核心原则：

1. **ADK Runtime 不承担平台后台职责。** Runtime 负责跑 Agent、Tool、Flow；Platform 负责用户、租户、模型、Skill、环境、审计、审批。
2. **PostgreSQL 是事实库。** Session/Event、Run、Approval、Skill 元数据、Model 元数据、Environment、Audit 都应该最终落 PostgreSQL。
3. **Redis 只做运行态。** SSE 续传、run continuation、短期锁、限流、缓存、任务队列可以放 Redis，但最终事实不能只在 Redis。
4. **MinIO/S3 只放大对象。** Artifact 文件、上传文件、Skill 包资源、大型 trace、环境命令输出、诊断报告放对象存储；元数据仍进 DB。
5. **Secret 永远不进入模型上下文。** 模型只看到环境 ID、能力、风险、命令预览和执行结果摘要，不看到 SSH key、token、kubeconfig、API key。

## 3. 推荐中间件组合

### 3.1 本地开发

```yaml
storage:
  session:
    type: filesystem
  artifact:
    type: filesystem
  memory:
    type: filesystem
```

适合快速调试、单进程、无外部依赖。

### 3.2 单机生产 / 小团队平台

```yaml
storage:
  database:
    type: postgres
  redis:
    enabled: true
  object:
    type: minio
  session:
    type: postgres
  artifact:
    type: minio
  memory:
    type: postgres
```

这是建议优先落地的形态。

### 3.3 企业版

```text
PostgreSQL + pgvector
Redis Cluster
MinIO / S3
Vault / KMS
OIDC / LDAP
Env Runner / Bastion / K8s in-cluster Runner
```

## 4. 模块边界

### 4.1 Auth / User / Tenant

职责：

- 解析用户身份；
- 为请求注入 `Principal`；
- 管理 tenant/user/role/api key；
- 控制模型、环境、Skill、Artifact、Memory 的访问边界。

第一版支持 `dev_token`，不用一开始做完整登录页。

```go
type Principal struct {
    TenantID string
    UserID   string
    Roles    []string
    Scopes   []string
}
```

后续所有平台服务都从 `context.Context` 读取 `Principal`，不要让前端随意传可信 `user_id`。

### 4.2 Session / Run

职责划分：

- `session.Service` 保存 ADK 会话、事件和状态；
- `runs` 保存一次执行的生命周期、状态、错误、模型、app、session 关联；
- Redis 保存运行中 continuation、SSE offset、短期状态。

不要用 `sessions` 表替代 `runs` 表。Session 是对话容器，Run 是一次执行过程。

### 4.3 Approval

职责：

- user choice；
- tool confirmation；
- environment operation approval；
- high-risk command approval；
- 表单确认。

统一用 `approval_requests` 表表达 pending/approved/rejected/expired，不要每个 Tool 自己发明一套审批状态。

### 4.4 Memory

职责：

- 管理长期可复用用户/项目记忆；
- 支持人工编辑、删除、归档；
- 支持从 session 提取 memory delta；
- 后续支持 embedding 检索。

Memory 不能只是 session 历史压缩。它应该是可审查、可编辑、可删除的知识条目。

### 4.5 Skill Registry

职责：

- 管理内置 Skill 和用户上传 Skill；
- 支持版本、发布、禁用、回滚；
- 管理 `SKILL.md` 和 `references/`、examples、schema 等资源；
- 给 Agent Loader 提供统一 skill resolution。

文件系统 Skill 仍保留为 builtin source，用户 Skill 用 DB + MinIO。

### 4.6 Model Registry

职责：

- 管理 provider、credential、model spec、alias；
- 支持 OpenAI-compatible、Gemini、后续本地模型；
- 前端只返回脱敏 metadata；
- 支持连通性测试；
- 支持 tenant/app/user/agent 级默认模型策略。

### 4.7 Environment Management

职责：

- 管理环境资产；
- 管理 Secret 引用；
- 管理 Operation Catalog；
- 执行前 risk analysis / preview / approval；
- 执行后 audit log 和 output object；
- EnvToolset 从服务读取环境，不再直接依赖 JSON 文件。

### 4.8 Artifact / Object

职责：

- 保存 Agent 输出文件；
- 保存用户上传文件；
- 保存 Skill 资源包；
- 保存大型 trace 和环境命令输出；
- 提供 metadata + object key 的统一索引。

## 5. 第一阶段开发范围

P0/P1 先解决“底座问题”，不要一开始把所有管理后台都写完。

```text
P0：Auth Principal + storage factory + sqlite session backend + 后续 postgres/mysql driver + migration 入口
P1：runs + approval_requests + run 状态持久化 + 刷新/暂停/恢复语义稳定
P2：Skill Registry DB+Object 版本化
P3：Model Registry DB 化和密钥引用
P4：Environment Store + Secret Store + Audit Store
P5：Memory DB 化和检索增强
```

详细任务见 `docs/backend-platform-implementation-plan.md`。
