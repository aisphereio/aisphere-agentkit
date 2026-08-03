# P1.1 Run / Approval Store 实现说明

本轮实现目标：先把“运行记录”和“人工审批”做成后台平台事实表和 REST API。它还没有完全接入真实 Agent Runtime 的每一次执行，但已经提供了后续接入刷新恢复、暂停、审批、审计的数据库底座。

## 1. 本轮新增能力

新增平台包：

```text
internal/platform/store
internal/platform/runs
internal/platform/approvals
```

新增 REST API：

```http
GET    /platform/runs
POST   /platform/runs
GET    /platform/runs/{run_id}
PATCH  /platform/runs/{run_id}
GET    /platform/runs/{run_id}/steps
POST   /platform/runs/{run_id}/steps
PATCH  /platform/run-steps/{step_id}

GET    /platform/approvals
POST   /platform/approvals
GET    /platform/approvals/{approval_id}
POST   /platform/approvals/{approval_id}/approve
POST   /platform/approvals/{approval_id}/reject
POST   /platform/approvals/{approval_id}/expire
```

注意：服务实际挂载在 API prefix 下面，默认访问路径是：

```text
/api/platform/runs
/api/platform/approvals
```

## 2. 数据表

当：

```yaml
storage:
  database:
    type: sqlite
    dsn: ./.adk/data/database/adk.db
    auto_migrate: true
```

启动 REST API 时会自动创建：

```text
runs
run_steps
approval_requests
```

## 3. Run 状态

当前支持：

```text
running
waiting_approval
completed
failed
cancelled
```

终态：

```text
completed
failed
cancelled
```

进入终态时会自动写入 `finished_at`。

## 4. Approval 状态

当前支持：

```text
pending
approved
rejected
expired
```

审批只允许从 `pending` 决策到 `approved/rejected/expired`。已经决策过的审批不能二次决策。

## 5. 验证方法

### 5.1 查看当前身份

```powershell
curl.exe http://localhost:8080/api/me
```

### 5.2 创建 run

```powershell
curl.exe -X POST http://localhost:8080/api/platform/runs `
  -H "Content-Type: application/json" `
  -d '{"app_name":"test1","session_id":"manual-session-1","input_summary":"manual run smoke test","model_ref":"default"}'
```

返回里记录 `id`，下面用 `$RUN_ID` 表示。

### 5.3 查询 run

```powershell
curl.exe http://localhost:8080/api/platform/runs
curl.exe http://localhost:8080/api/platform/runs/$RUN_ID
```

### 5.4 创建 step

```powershell
curl.exe -X POST http://localhost:8080/api/platform/runs/$RUN_ID/steps `
  -H "Content-Type: application/json" `
  -d '{"kind":"llm","payload_json":"{\"note\":\"first step\"}"}'
```

### 5.5 把 run 标记为等待审批

```powershell
curl.exe -X PATCH http://localhost:8080/api/platform/runs/$RUN_ID `
  -H "Content-Type: application/json" `
  -d '{"status":"waiting_approval"}'
```

### 5.6 创建 approval

```powershell
curl.exe -X POST http://localhost:8080/api/platform/approvals `
  -H "Content-Type: application/json" `
  -d '{"run_id":"'$RUN_ID'","kind":"environment_operation","payload_json":"{\"operation_id\":\"kubectl_get_pods\",\"risk_level\":\"L1\"}"}'
```

返回里记录 `id`，下面用 `$APPROVAL_ID` 表示。

### 5.7 审批通过

```powershell
curl.exe -X POST http://localhost:8080/api/platform/approvals/$APPROVAL_ID/approve `
  -H "Content-Type: application/json" `
  -d '{"reason":"同意执行只读诊断"}'
```

### 5.8 把 run 标记完成

```powershell
curl.exe -X PATCH http://localhost:8080/api/platform/runs/$RUN_ID `
  -H "Content-Type: application/json" `
  -d '{"status":"completed"}'
```

## 6. 当前还没接入的地方

本轮只是后台事实表和 API，尚未把真实 runtime 流程完全写入这些表。

后续接入点：

```text
server/adkrest/controllers/runtime.go
server/adkrest/internal/resumable/store.go
环境管理 EnvToolset 审批分支
前端审批卡片
SSE event 中输出 run_id / approval_id
```

下一轮建议：

1. 在 `RunSSE` 创建真实 run；
2. 返回响应头 `X-ADK-Run-ID`；
3. SSE 第一帧输出 run metadata；
4. runtime 完成/失败时更新 run 状态；
5. tool confirmation / env approval 创建 approval request。
