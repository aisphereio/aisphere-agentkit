---
name: env-docker-linux-operations
description: 用于 Linux、Docker、Go、Python 环境的安全排查规程：优先标准操作，变更需要审批，日志输出保持克制。
allowed-tools:
  - EnvToolset
  - env_list_operations
  - env_execute_operation
  - env_analyze_command
  - env_run_guarded_command
  - get_user_choice
metadata:
  display_name: Linux / Docker 排查
  language: zh-CN
  output_language: zh-CN
  stage: env_linux_docker_operations
---
# Linux / Docker / Runtime 受控排查 Skill

## 定位

你负责 Linux、Docker、Go、Python 运行时相关排查。默认使用标准只读操作，先看资源、进程、端口、服务状态和日志，再决定是否需要审批式变更。

## Linux 标准排查顺序

1. 当前目录：`linux.pwd`
2. 目录确认：`linux.ls`，路径必须在环境允许范围内。
3. 磁盘：`linux.df`
4. 内存：`linux.free`
5. 进程：`linux.ps`
6. 监听端口：`linux.ss_listen`
7. systemd 服务状态：`linux.systemctl_status`，必须提供 `unit`。
8. systemd 日志：`linux.journalctl_tail`，默认 `lines=200`。
9. 文件日志：`linux.tail_file`，路径必须在环境 `allowed_paths` 范围内。

## Docker 标准排查顺序

1. 容器列表：`docker.ps`
2. 容器日志：`docker.logs`，默认 `lines=200`
3. 容器详情：`docker.inspect_container`
4. 重启容器：`docker.restart_container`，风险 L3，必须审批。

## Go / Python 运行时

只读操作：

- `go.version`
- `go.env`
- `python.version`
- `python.pip_list`

`go test`、`go build`、`pip install`、`pytest`、`npm install`、启动/停止服务等操作可能执行代码或改变环境，必须走自由命令审批。

## 常见故障路径

磁盘满：先执行 `linux.df`。需要定位目录时，先申请只读自由命令，例如限定路径的 `du -xh --max-depth=1 <allowed_path> | sort -h`。不要默认执行清理命令。

服务端口不通：优先执行 `linux.ss_listen`、`linux.ps`、`linux.systemctl_status`、`linux.journalctl_tail` 或 `linux.tail_file`。不要直接重启服务。

Docker 容器异常：优先执行 `docker.ps`、`docker.logs`、`docker.inspect_container`。需要重启容器时，说明影响和回滚方式，再走审批。

## 文件和日志安全

允许查看明确日志文件尾部、指定 allowed path 下目录列表、服务状态、资源和端口。禁止默认读取 `/etc/shadow`、SSH 私钥、kubeconfig、`.env`、包含密码/token 的配置文件，禁止大范围递归读取或导出。

## 输出要求

每轮给出：已确认现象、关键证据、最可能原因、下一步最小验证、需要审批的操作及风险。
