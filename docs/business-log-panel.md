# 业务日志查看面板

业务日志查看面板是 Chat UI 内的辅助窗口，用于查看真实业务或环境日志。它和 runtime trace 日志不同：runtime trace 解释 Agent 如何运行，业务日志解释被管理环境中 Redis、Docker、Kubernetes、应用服务本身发生了什么。

## 后端接口

```http
GET /api/business_logs/stream?environment_id=dev&kind=docker&container=redis&tail=200&follow=true
```

支持的 `kind`：

- `docker`：参数 `container`
- `k8s`：参数 `namespace`、`pod`、可选 `k8s_container`
- `file`：参数 `path`
- `journal`：参数 `unit`

SSE 事件类型：

```json
{"type":"business.log.start","stream_id":"...","target":"redis"}
{"type":"business.log.line","line":"...","source":"stdout"}
{"type":"business.log.error","message":"..."}
{"type":"business.log.done","message":"log stream completed"}
```

## 前端能力

`app-business-log-panel` 支持：

- 实时显示；
- 暂停；
- 停止；
- 过滤；
- 高亮；
- 自动滚动；
- 清空窗口。

## 当前边界

第一版使用 `agents/env_manager/env/environments.example.json` 作为环境配置来源。后续环境资产服务化后，应改为从平台 Environment Store 读取环境、Secret 和审计策略。

第一版业务日志面板是用户手动打开。后续可以让 EnvToolset 的日志类 tool result 返回 `ui.renderer=business_log_panel`，由前端自动弹出该面板。
