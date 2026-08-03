---
name: ui-business-log-panel
description: 定义聊天界面的业务日志查看协议，用于展示 Docker、Kubernetes、文件 tail 和 journal 等真实运行日志。
metadata:
  display_name: 业务日志面板
  language: zh-CN
  output_language: zh-CN
  category: frontend-observability
  tags: ui,business-log,docker,k8s,tail,journal
---
# 业务日志查看面板 Skill

## 定位

业务日志面板用于查看真实业务/环境日志，例如：

- Docker 容器日志：`docker logs -f redis`
- Kubernetes Pod 日志：`kubectl logs -f pod -n namespace`
- Linux 文件日志：`tail -F /path/to/app.log`
- systemd 服务日志：`journalctl -u redis -f`

它不是 Agent runtime trace，也不是模型 token 流。

## 使用规则

当用户提出“查看某个容器/Pod/文件/服务日志”时：

1. Agent 应先确认目标环境、日志类型和目标对象。
2. 如果目标明确，应优先触发业务日志面板，而不是把大段日志粘贴进聊天正文。
3. 聊天正文只做摘要说明，例如“已打开 redis 容器日志窗口”。
4. 日志窗口负责实时展示、过滤、暂停、高亮、停止。
5. 如果日志中出现错误、异常、超时、panic、failed 等关键字，可以提示用户是否让审查 Agent 总结问题。

## 前端交互

业务日志面板应支持：

- 实时流式显示；
- 暂停显示，但不中断后端连接；
- 停止连接；
- 关键字过滤；
- 正则高亮；
- 自动滚动开关；
- 清空当前窗口；
- 后续可扩展“让 LLM 总结当前日志”。

## 和 runtime.log 的区别

- `runtime.log`：Agent 执行过程日志，例如 agent.enter、tool.call。
- `business.log.*`：业务系统日志，例如 Redis、Nginx、Pod、应用日志。

两者应使用不同 UI 组件展示。
