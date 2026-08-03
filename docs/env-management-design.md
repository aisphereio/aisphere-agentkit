# 环境管理能力设计与落地 TODO

## 1. 设计目标

环境管理能力用于让 Agent 协助用户排查和维护 Linux、Docker、Kubernetes、Golang、Python 等运行环境。平台不把模型变成裸 shell，而是提供三层自由度：标准操作、受控命令风险分析、审批式自由命令。

核心原则：模型负责理解、计划、解释；平台负责权限、执行、审批、审计、脱敏。

## 2. 当前已落地

- `tool/envmanagertool`：受控环境管理 Toolset。
- `EnvToolset`：可在 YAML Agent 配置里注册。
- `agents/env_manager/root_agent.yaml`：环境管理 Agent。
- `agents/env_manager/env/environments.example.json`：环境资产清单。
- `skills/env-management-core`：环境管理核心规程。
- `skills/env-k8s-operations`：K8s 受控排查规程。
- `skills/env-docker-linux-operations`：Linux/Docker/Runtime 受控排查规程。

## 3. EnvToolset 工具

- `env_list_environments`：列出环境，不暴露密钥。
- `env_list_operations`：列出标准操作目录。
- `env_analyze_command`：分析自由命令风险，不执行。
- `env_execute_operation`：执行标准操作，受 capability、risk、safety_mode、approval、dry-run 约束。
- `env_run_guarded_command`：审批式执行自由命令。

## 4. 真实 SSH 环境接入

当前支持 `local` 和 `ssh` 两种 `connection_type`。SSH 已支持：

- `key_path`：平台进程本地私钥路径。
- `password_env`：密码环境变量名。配置文件只保存变量名，不保存密码原文。

真实环境配置示例：

```json
{
  "environment_id": "hongmei-root-ssh",
  "connection_type": "ssh",
  "host": "CHANGE_ME_HOST",
  "port": 22,
  "username": "root",
  "password_env": "ADK_ENV_HONGMEI_ROOT_PASSWORD",
  "allow_execute": true,
  "capabilities": ["linux.read", "docker.read", "go.read", "python.read"]
}
```

启动后端前，把密码放进后端进程环境变量：

```powershell
$env:ADK_ENV_HONGMEI_ROOT_PASSWORD = "<password>"
```

不要把密码写入 YAML、JSON、artifact、memory、trace 或日志。

## 5. 安全模式

- `observe`：只允许 L0/L1 只读操作。
- `safe_approval`：L1 可执行，L2+ 需要审批。
- `strict_approval`：所有环境操作都需要审批。
- `maintenance_window`：维护窗口内放宽低风险操作，L3+ 仍需审批。
- `expert`：专家模式，L4 仍需二次确认。

## 6. 自由度等级

- `F0`：不执行环境命令。
- `F1`：只允许标准只读操作。
- `F2`：允许更多标准诊断操作。
- `F3`：允许只读自由命令审批执行。
- `F4`：允许审批式写操作。
- `F5`：允许维护窗口/高危审批操作。

## 7. 当前边界

- 已支持文件型环境清单，后续应迁移到 EnvironmentService。
- 已支持工具返回审计元数据，后续应写入持久化 Audit Store。
- 写操作已有审批 payload，前端还需要继续优化审批卡展示。
- 当前 SSH host key 使用平台侧快速接入策略；生产化应增加 known_hosts/指纹校验。

## 8. 后续服务化形态

```text
EnvToolset
  ├── EnvironmentService      环境资产、capability、safety_mode、freedom_level
  ├── SecretService           SSH key、password、token、kubeconfig 的 secret_ref
  ├── OperationCatalogService 标准操作目录和参数 schema
  ├── ApprovalService         高风险/写操作审批
  └── AuditService            持久化审计和输出对象索引
```

生产环境目标：模型只看到脱敏 metadata；Secret 只以 `secret_ref` 或 `password_env` 参与执行，不进入 prompt、trace、memory、artifact。
