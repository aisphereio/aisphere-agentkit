# Skill Asset Service 落地说明

本次改造把原来的文件系统 Skill CRUD 升级为文件系统版 Skill Asset Service。目标是先跑通管理流程，后续 S3/MinIO/DB 只替换 repository/storage 实现。

## 组件边界

- `adk-go`：唯一可信后端，负责 Skill service、HTTP API、状态/权限元数据、校验、引用扫描、导入导出、运行时加载。
- `adk-admin`：资产管理入口，负责创建、编辑、导入、导出、校验、发布、废弃、归档、资源管理、引用关系查看。
- `adk-web`：业务使用入口，只消费已发布、已授权、可运行的 Agent；不直接编辑 Skill。

## 文件系统存储

每个 Skill 是一个目录：

```text
skills/<skill-name>/
  SKILL.md
  .skill-meta.json
  references/
  assets/
  scripts/
```

- `SKILL.md`：能力说明、frontmatter、instructions、allowed-tools 等内容定义。
- `.skill-meta.json`：平台生命周期和权限元数据，例如 owner、visibility、status、created_by、permissions。

## 生命周期

支持状态：

- `draft`：草稿，默认不应进入生产 Agent。
- `published`：已发布，可被 Agent 引用和运行时解析。
- `deprecated`：已废弃，不建议新引用，保留兼容。
- `archived`：归档，不建议展示和新运行。
- `deleted`：逻辑删除，列表默认不展示。

删除策略：

- draft 且无引用，可以物理删除。
- 被 Agent/Flow 引用时，默认删除返回 409。
- published/deprecated/archived 默认走逻辑删除或归档，不直接物理删除。

## API 能力

新增/增强：

```text
GET    /skills
GET    /skills/{name}
POST   /skills
PUT    /skills/{name}
DELETE /skills/{name}?force=false&physical=false
POST   /skills/import
GET    /skills/{name}/export
POST   /skills/{name}/validate
GET    /skills/{name}/references
POST   /skills/{name}/status
POST   /skills/{name}/publish
POST   /skills/{name}/deprecate
POST   /skills/{name}/archive
GET    /skills/{name}/resources
GET    /skills/{name}/resources/{path}
POST   /skills/{name}/resources
PUT    /skills/{name}/resources
DELETE /skills/{name}/resources/{path}
```

## 运行时关系

当前仍保持兼容：Agent YAML 通过 `skills:` 引用 Skill name，Runtime 继续通过 `skill.Source` 加载。后续可继续抽出 `SkillResolver`，在运行前做：

1. Agent 是否已发布和授权。
2. Skill 是否存在、published/deprecated、用户是否有 `skill.use`。
3. Tool 是否存在和授权。
4. 生成 RunSnapshot：记录 Agent/Skill/Tool 版本与 hash。

## 后续建议

1. 引入 `SkillRepository` 接口，把当前 `FileSystemService` 拆成 Service + Repository。
2. 引入 Agent Registry，扫描 `root_agent.yaml` 并展示 Agent -> Skill 引用。
3. 引入 Project Agent Binding，让 adk-web 只显示当前项目可运行 Agent。
4. Skill 发布生成不可变版本，Run 记录 resolved Skill version/hash。
5. 增加 S3/MinIO repository：`s3://agentkit-assets/{workspace}/skills/{skill}/...`。
