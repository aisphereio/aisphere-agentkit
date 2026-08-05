# GoRunner 闭环运行说明

这条链路把 Hub、AgentKit Runtime、沙箱和 ADK-Go loop 串成一个可部署的最小产品闭环：

```text
Hub /v1/agents/{id}:resolve
  -> immutable Agent snapshot（agent prompt、model、skills、tools、authorization）
  -> RuntimePlan
  -> sandbox lease（只拿工具执行入口，不把 Hub 凭证下发给沙箱）
  -> ADK-Go Runner / session loop
  -> permission gate
  -> sandbox tool / MCP toolset
  -> session events / REST / SSE
```

## 配置

GoRunner 让 AgentKit 自己复用 ADK-Go 的 loop；沙箱只负责工作区和工具执行。

```yaml
skills:
  enabled: true
  root: ./skills
  preload: complete
  aihub:
    enabled: true
    endpoint: http://aisphere-hub:8848
    runtime_id: service:agentkit-runtime
    token_env: AIHUB_RUNTIME_TOKEN
    sandbox:
      enabled: true
      mode: agent-native
      native_session: true
      go_runner: true
      adapter_endpoint: http://aisphere-sandbox:18082
      adapter_token_env: AISPHERE_SANDBOX_TOKEN
      default_profile: default-python-offline
      ready_timeout_seconds: 90

mcp:
  servers:
    novel_assets:
      transport: streamable_http
      endpoint: http://novel-assets-mcp:8090/mcp
      headers:
        Authorization: Bearer ${NOVEL_ASSETS_MCP_TOKEN}
      enabled: true
```

MCP endpoint、凭证和网络配置属于 Runtime 配置；Agent 在 Hub 里只绑定 MCP server/tool。这样 Hub 决定“能不能用”，Runtime 决定“如何连”，ADK 决定“何时调用”。

## 一次运行

如果 Agent 有 `per_run` 工具，前端先调用：

```http
POST /v1/agents/{agentId}:plan-run
```

用户确认后调用 AgentKit 的 `/run` 或 `/run_sse`，把以下字段原样带上：

```json
{
  "appName": "research-agent",
  "userId": "user-1",
  "sessionId": "session-1",
  "approvalConfirmed": true,
  "approvedTools": ["workspace.read", "list_split_books"],
  "newMessage": {
    "role": "user",
    "parts": [{"text": "读取项目资料并给出摘要"}]
  }
}
```

Runtime 会把审批字段转交 Hub。Hub 返回被授权并版本固定的 snapshot；Runtime 不在本地扩大工具权限。`approvalMode=disabled` 的工具不会进入 snapshot，`per_run` 未确认时运行直接失败并返回审批错误。

## Skill 的实际位置

- Hub 只保存 Agent 选择的 skill 引用和版本。
- Runtime 用 Hub snapshot 下载并校验 catalog skill；内置 skill 必须已经在 `skills.root` 中。
- GoRunner 为每个 session 生成隔离目录：`.aihub/sessions/<session>/runtime-skills/<skill>/`。
- ADK `SkillToolset` 只暴露当前 snapshot 的 skill，并把 frontmatter/指令挂入模型请求上下文。
- 沙箱另有 `.aisphere/skills/<skill>/` 挂载，供沙箱工具和工作区脚本读取；沙箱不需要 Hub token。

## Tool 的执行位置

| 类型 | 模型可见 | 实际执行 | 权限边界 |
| --- | --- | --- | --- |
| `sandbox` | RuntimePlan 中的 schema | `Sandbox Adapter /tools/call` | Runtime permission gate + 沙箱策略 |
| `mcp` | ADK lazy toolset 发现的远程工具 | MCP server | Runtime MCP registry + Hub allowlist |
| `internal` | AgentKit 注册的工具 | AgentKit 进程 | Runtime 内置策略 |

工具解析失败时 Runtime fail closed，不会把未注册的 runtime type 暴露给模型。MCP toolset 在首次请求时才连接服务，避免 session 创建阶段阻塞。

## 当前可验收标准

1. Hub resolve 返回 agent definition、model、skills、tools、authorization。
2. session 创建成功后能看到 `runtimePlan` 和 sandbox lease state。
3. `/run` 能跑通 ADK-Go loop；模型发起 sandbox tool call 后，调用经过 permission gate 并到达 sandbox adapter。
4. `/run_sse` 能持续输出模型、工具响应和 `adkRunDone`。
5. MCP server 缺失、skill 未 materialize、工具未注册、权限未通过时均明确失败，不回退到未授权执行。

本地最小验证：

```powershell
go test ./internal/runtimeexecutor ./internal/sessionnative ./internal/skillruntime ./internal/mcpruntime ./internal/permissiongate ./server/adkrest/controllers -count=1
```
