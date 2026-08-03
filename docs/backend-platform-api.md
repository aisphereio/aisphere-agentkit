# 后台平台 API 草案

> 本文档定义下一阶段 REST API 形态。第一版可以只实现 P0/P1 的一部分，但 URL、对象命名和返回结构应尽量保持稳定。

## 1. 通用约定

### 1.1 鉴权

第一版建议支持 `dev_token`：

```http
Authorization: Bearer <token>
```

服务端解析成 `Principal`：

```json
{
  "tenant_id": "default",
  "user_id": "admin",
  "roles": ["owner"],
  "scopes": ["*"]
}
```

后续可扩展 local user login / OIDC / API key。

### 1.2 错误返回

```json
{
  "error": {
    "code": "invalid_argument",
    "message": "app_name is required",
    "details": {}
  }
}
```

建议 code：

```text
unauthenticated / permission_denied / invalid_argument / not_found / conflict / failed_precondition / internal
```

## 2. 用户与权限

P0 已实现 `/me`；P1.2 新增 PG-backed tenant/user MVP。登录页、密码登录、API Key 仍是后续任务。

```http
GET  /me
GET  /platform/tenant
POST /platform/tenants
GET  /platform/users
POST /platform/users
GET  /platform/users/{user_id}
PATCH /platform/users/{user_id}
```

`GET /me` 返回：

```json
{
  "tenant_id": "default",
  "user_id": "admin",
  "roles": ["owner"],
  "scopes": ["*"],
  "auth_mode": "dev_token"
}
```

`GET /platform/users` 返回当前 tenant 下的 durable user records。服务启动时会把 `auth.mode=none` 的 `default/admin/owner` 或 `auth.dev_tokens` 中的 principal 引导写入 PG。

后续接口：

```http
POST /auth/login
POST /auth/logout
GET  /roles
POST /api-keys
DELETE /api-keys/{id}
```

## 2.5 Project / Workbench

P1.2 新增 durable project/workbench 记录，用来承载“项目级状态”，不再只依赖前端本地缓存或文件目录。

```http
GET    /platform/projects?app_name=&owner_user_id=&status=&limit=
POST   /platform/projects
GET    /platform/projects/{project_id}
PATCH  /platform/projects/{project_id}
POST   /platform/projects/{project_id}/archive
```

创建项目示例：

```json
{
  "name": "demo-project",
  "display_name": "Demo Project",
  "app_name": "test1",
  "metadata_json": "{"source":"manual"}"
}
```

## 3. Session / Run

保留当前 ADK sessions API：

```http
GET    /apps/{app_name}/users/{user_id}/sessions
POST   /apps/{app_name}/users/{user_id}/sessions
GET    /apps/{app_name}/users/{user_id}/sessions/{session_id}
DELETE /apps/{app_name}/users/{user_id}/sessions/{session_id}
```

平台化新增：

```http
GET /sessions?app_name=&user_id=
GET /sessions/{id}
GET /sessions/{id}/events
```

Run API（P1.1 MVP 已实现，挂载在 `/api/platform` 下）：

```http
GET    /platform/runs?app_name=&session_id=&status=&limit=
POST   /platform/runs
GET    /platform/runs/{run_id}
PATCH  /platform/runs/{run_id}
GET    /platform/runs/{run_id}/steps
POST   /platform/runs/{run_id}/steps
PATCH  /platform/run-steps/{step_id}
```

`GET /platform/runs/{run_id}` 示例：

```json
{
  "id": "run_01",
  "tenant_id": "default",
  "app_name": "novel_opening",
  "user_id": "admin",
  "session_id": "session_01",
  "status": "waiting_approval",
  "model_ref": "deepseek_chat",
  "started_at": "2026-05-30T12:00:00Z",
  "finished_at": null,
  "error_message": ""
}
```

## 4. Approval

```http
GET  /platform/approvals?status=pending
POST /platform/approvals
GET  /platform/approvals/{approval_id}
POST /platform/approvals/{approval_id}/approve
POST /platform/approvals/{approval_id}/reject
POST /platform/approvals/{approval_id}/expire
```

`environment_operation` approval payload：

```json
{
  "kind": "environment_operation",
  "title": "执行 Kubernetes 诊断命令",
  "payload": {
    "environment_id": "prod-k8s-a",
    "operation_id": "kubectl_get_pods",
    "command_preview": "kubectl get pods -n default",
    "risk_level": "L1",
    "read_only": true,
    "requires_approval": true,
    "reason": "strict_approval requires approval for all operations"
  }
}
```

Approve：

```http
POST /platform/approvals/{approval_id}/approve
Content-Type: application/json

{
  "reason": "同意执行只读诊断"
}
```

Reject：

```http
POST /platform/approvals/{approval_id}/reject
Content-Type: application/json

{
  "reason": "生产环境先不要执行"
}
```

## 5. Memory

```http
GET    /memory?app_name=&user_id=&type=&status=active
POST   /memory/search
POST   /memory
PATCH  /memory/{id}
DELETE /memory/{id}
POST   /memory/from-session
```

Search 请求：

```json
{
  "app_name": "novel_opening",
  "user_id": "admin",
  "query": "用户对都市升级小说的偏好",
  "limit": 10,
  "types": ["preference", "project_context"]
}
```

Memory entry：

```json
{
  "id": "mem_01",
  "type": "preference",
  "content": "用户偏好男频都市升级，强调黄金三章、爽点节奏和回旋镖校验。",
  "confidence": 0.95,
  "importance": 8,
  "status": "active"
}
```

## 6. Skill Registry

保留当前 `/skills` 只读能力，新增管理接口：

```http
GET    /skills
GET    /skills/{id}
POST   /skills
PATCH  /skills/{id}
DELETE /skills/{id}
GET    /skills/{id}/versions
POST   /skills/{id}/versions
POST   /skills/{id}/versions/{version}/publish
POST   /skills/{id}/versions/{version}/deprecate
GET    /skills/{id}/resources
```

创建 Skill：

```json
{
  "name": "novel-commercial-contract",
  "display_name": "小说商业合同设计",
  "description": "用于约束开篇读者承诺、弃书防线和爽点兑现策略。",
  "category": "novel",
  "visibility": "tenant"
}
```

创建版本：

```json
{
  "version": "0.1.0",
  "changelog": "initial version",
  "skill_md": "---\nid: novel-commercial-contract\n---\n# ..."
}
```

后续支持 multipart 上传资源包。

## 7. Model Registry

```http
GET    /models
GET    /models/providers
POST   /models/providers
POST   /models/credentials
POST   /models/specs
PATCH  /models/specs/{id}
POST   /models/specs/{id}/test
POST   /models/aliases
DELETE /models/aliases/{id}
```

`GET /models` 必须脱敏：

```json
{
  "default": "deepseek_chat",
  "aliases": {
    "default": "deepseek_chat"
  },
  "specs": [
    {
      "id": "deepseek_chat",
      "provider": "openai",
      "model": "deepseek_v4",
      "base_url_present": true,
      "api_key_present": true,
      "context_window": 100000,
      "supports_tools": true,
      "supports_streaming": true
    }
  ]
}
```

注意：不要返回 `api_key`、`password`、`secret_ref` 的明文。

## 8. Environment Management

```http
GET    /environments
POST   /environments
GET    /environments/{id}
PATCH  /environments/{id}
DELETE /environments/{id}
GET    /environments/{id}/operations
POST   /environments/{id}/operations/{operation_id}/preview
POST   /environments/{id}/operations/{operation_id}/execute
POST   /environments/{id}/commands/analyze
POST   /environments/{id}/commands/execute
GET    /environment-audits
GET    /environment-audits/{id}
```

创建环境：

```json
{
  "name": "prod-linux-01",
  "type": "linux",
  "connection_type": "ssh",
  "host": "10.0.0.11",
  "port": 22,
  "username": "root",
  "secret_ref": "sec_prod_linux_ssh_key",
  "safety_mode": "safe_approval",
  "freedom_level": "F2",
  "allow_execute": false,
  "capabilities": ["linux.read", "docker.read"]
}
```

Preview：

```json
{
  "parameters": {
    "namespace": "default"
  },
  "dry_run": true
}
```

返回：

```json
{
  "plan": {
    "environment_id": "prod-k8s-a",
    "operation_id": "kubectl_get_pods",
    "command": "kubectl get pods -n default",
    "risk_level": "L1",
    "read_only": true,
    "requires_approval": false,
    "blocked": false
  }
}
```

Execute：

```json
{
  "parameters": {
    "namespace": "default"
  },
  "dry_run": false,
  "approval_id": "appr_01"
}
```

## 9. Artifact / Object

保留当前 artifact API，同时新增平台索引接口：

```http
GET  /artifacts?app_name=&session_id=&user_id=
GET  /artifacts/{id}
GET  /artifacts/{id}/versions
GET  /artifacts/{id}/versions/{version}/download
POST /uploads
```

上传大文件走对象存储，DB 只存 metadata。

## 10. 与现有 API 的兼容策略

| 现有 API | 保留 | 变化 |
| --- | --- | --- |
| `/run_sse` / runtime SSE | 是 | 增加 run_id、approval_id、trace_id |
| `/apps/*/sessions` | 是 | 内部加入 tenant/principal 权限过滤 |
| `/skills` | 是 | 从 filesystem service 逐步切换到 registry 聚合 |
| `/models` metadata | 是 | 从 YAML-only 切换到 DB + YAML fallback |
| `/artifacts` | 是 | 增加 MinIO/S3 backend 和 metadata index |
