# SkillHub 能力迁移到 ADK Go 后端说明

本次改造目标：不引入 SkillHub Java 服务，也不强依赖数据库，直接把 SkillHub 中对 Skill 管理最有价值的一层能力迁移到 ADK 当前 Go 后端和 `agentkit-admin`。

## 后端实现位置

- `agentkit/internal/skillservice/service.go`
- `agentkit/server/adkrest/controllers/skills.go`
- `agentkit/server/adkrest/internal/routers/skills.go`

## 前端实现位置

- `agentkit-admin/src/lib/api.ts`
- `agentkit-admin/src/components/admin/skills/skill-management.tsx`

## 设计取舍

SkillHub 原项目有 Java 后端、数据库、命名空间、审核流、版本表、标签表、下载统计、举报治理等完整 Hub 能力。ADK 当前项目更适合作为 Agent 平台内置 Skill 仓库，所以这版采用文件系统实现：

- `skills/<skill-id>/SKILL.md` 仍然是唯一核心事实源。
- version/status/visibility/category/owner/changelog/labels/tags 等 SkillHub 风格字段写入 `SKILL.md` frontmatter 的 `metadata` 中。
- 不引入 Java 服务，不引入新数据库迁移。
- Agent 运行时仍然通过 ADK 原生 `skill.Source` 加载 skill。

## 新增/增强 REST API

默认挂在 ADK REST 前缀下，例如前端配置 `/api` 时实际访问 `/api/skills`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/skills?q=&label=&tag=&tool=&status=&visibility=&category=` | Skill 列表 + 搜索筛选 |
| GET | `/skills/{name}` | Skill 详情，包含 instructions/rawMarkdown/resources |
| POST | `/skills` | 创建 Skill |
| PUT | `/skills/{name}` | 更新 Skill |
| DELETE | `/skills/{name}` | 删除 Skill 目录 |
| POST | `/skills/import` | 导入 zip 包或单个 SKILL.md，multipart 字段为 `file` |
| GET | `/skills/{name}/export` | 导出该 Skill 为 zip |
| GET | `/skills/{name}/resources` | 列出资源文件 |
| GET | `/skills/{name}/resources/{path}` | 读取资源文件 |
| POST/PUT | `/skills/{name}/resources` | 保存资源文件，JSON: `{ "path": "references/a.md", "content": "...", "encoding": "plain" }` |
| DELETE | `/skills/{name}/resources/{path}` | 删除资源文件 |

## Frontmatter metadata 约定

示例：

```yaml
---
name: env-docker-linux-operations
description: Docker/Linux 运维只读与诊断 Skill
allowed-tools:
  - shell_exec
metadata:
  version: 0.1.0
  status: published
  visibility: internal
  category: ops
  owner: platform
  labels: operations,docker
  tags: latest
---
```

## 当前边界

这版没有复制 SkillHub 的完整商业 Hub 能力：

- 没有数据库版本表；版本字段只是 metadata。
- 没有审核任务表；status 只是管理状态展示。
- 没有真实 namespace/RBAC；仍复用 ADK 当前 auth middleware。
- 没有下载统计、收藏、评分、举报。

后续如果要继续 SkillHub 化，建议第二阶段把 `skillservice` 抽成 repository 接口，补 Postgres 模型：`skill`, `skill_version`, `skill_file`, `skill_label`，再把当前 filesystem service 作为 local dev adapter 保留。
