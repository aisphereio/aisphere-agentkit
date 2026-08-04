# Agent Development Kit (ADK) for Go

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Doc](https://img.shields.io/badge/Go%20Package-Doc-blue.svg)](https://pkg.go.dev/google.golang.org/adk)
[![Nightly Check](https://github.com/google/adk-go/actions/workflows/nightly.yml/badge.svg)](https://github.com/google/adk-go/actions/workflows/nightly.yml)
[![r/agentdevelopmentkit](https://img.shields.io/badge/Reddit-r%2Fagentdevelopmentkit-FF4500?style=flat&logo=reddit&logoColor=white)](https://www.reddit.com/r/agentdevelopmentkit/)
[![View Code Wiki](https://www.gstatic.com/_/boq-sdlc-agents-ui/_/r/YUi5dj2UWvE.svg)](https://codewiki.google/github.com/google/adk-go)

<html>
    <h2 align="center">
      <img src="https://raw.githubusercontent.com/google/adk-python/main/assets/agent-development-kit.png" width="256"/>
    </h2>
    <h3 align="center">
      An open-source, code-first Go toolkit for building, evaluating, and deploying sophisticated AI agents with flexibility and control.
    </h3>
    <h3 align="center">
      Important Links:
      <a href="https://google.github.io/adk-docs/">Docs</a> &
      <a href="https://github.com/google/adk-go/tree/main/examples">Samples</a> &
      <a href="https://github.com/google/adk-python">Python ADK</a> &
      <a href="https://github.com/google/adk-java">Java ADK</a> & 
      <a href="https://github.com/google/adk-web">ADK Web</a>.
    </h3>
</html>

Agent Development Kit (ADK) is a flexible and modular framework that applies software development principles to AI agent creation. It is designed to simplify building, deploying, and orchestrating agent workflows, from simple tasks to complex systems. While optimized for Gemini, ADK is model-agnostic, deployment-agnostic, and compatible with other frameworks.

This Go version of ADK is ideal for developers building cloud-native agent applications, leveraging Go's strengths in concurrency and performance.

---

## ✨ Key Features

*   **Idiomatic Go:** Designed to feel natural and leverage the power of Go.
*   **Rich Tool Ecosystem:** Utilize pre-built tools, custom functions, or integrate existing tools to give agents diverse capabilities.
*   **Code-First Development:** Define agent logic, tools, and orchestration directly in Go for ultimate flexibility, testability, and versioning.
*   **Modular Multi-Agent Systems:** Design scalable applications by composing multiple specialized agents.
*   **Deploy Anywhere:** Easily containerize and deploy agents, with strong support for cloud-native environments like Google Cloud Run.

## 🚀 Installation

To add ADK Go to your project, run:

```bash
go get google.golang.org/adk
```

---

## Project extension docs

This fork contains additional platformization documents for the ADK Go backend:

- `docs/runtime-config.md` — Viper-backed runtime config, model aliases, and storage factory notes.
- `docs/openai-filesystem.md` — OpenAI-compatible adapter and local filesystem storage.
- `docs/skill-professionalization-20260529.md` — Skill package professionalization and business skill extraction.
- `docs/env-management-design.md` — guarded environment-management toolset design.
- `docs/backend-platform-design.md` — backend platform capability design for users, sessions, memory, skills, models, environments, approvals, and artifacts.
- `docs/backend-platform-data-model.md` — database model draft.
- `docs/backend-platform-api.md` — management API draft.
- `docs/backend-platform-config-examples.md` — local and production config examples.
- `docs/backend-platform-session-database.md` — database-backed session backend, now including PostgreSQL.
- `docs/backend-platform-postgres-storage.md` — P1.3 PostgreSQL storage backend, auto-create database, and verification steps.
- `docs/backend-platform-implementation-plan.md` — long-running implementation roadmap.
- `docs/backend-platform-handoff.md` — handoff notes for the next development agent/Codex session.
- `docs/go-runner-closed-loop.md` — Hub snapshot、skill/tool/MCP 装配、权限闸门和 GoRunner 闭环运行说明。


## 📄 License

This project is licensed under the Apache 2.0 License - see the
[LICENSE](LICENSE) file for details.

The exception is internal/httprr - see its [LICENSE file](internal/httprr/LICENSE).

### Backend platform P1.1: Run / Approval Store MVP

This branch adds the first platform database services for execution lifecycle and human-in-the-loop approvals.

New local API endpoints under the REST API prefix:

```text
GET/POST/PATCH /api/platform/runs
GET/POST       /api/platform/runs/{run_id}/steps
GET/POST       /api/platform/approvals
POST           /api/platform/approvals/{approval_id}/approve
POST           /api/platform/approvals/{approval_id}/reject
```

See `docs/backend-platform-run-approval.md` for curl examples and validation steps.

### Agent Runtime Observability

The runtime trace layer now records normalized execution events for runner, agent, model, and tool boundaries. See `docs/agent-runtime-observability.md` for the event taxonomy and verification steps.

## Agent 自提升治理闭环

本版本新增一组平台治理 Agent：`agent_ops`。它用于基于真实运行现场生成可审核的改进建议，而不是让业务 Agent 自动修改自己。

流程：

```text
用户反馈 / trace / artifact / tool error
  -> objective_review_agent 生成 agent_improvement_issue.md
  -> self_improvement_agent 生成 agent_improvement_proposal.md
  -> approval_packet_agent 生成 agent_improvement_approval_packet.md
  -> 人类审核后再应用变更
```

同时，Agent YAML 开始使用 `metadata.role` 标记角色，例如 `entry_router`、`workflow`、`worker`、`objective_reviewer`、`self_improver`、`approval_packet_builder`。详见：

- `docs/agent-self-improvement-loop.md`
- `docs/agent-role-governance.md`

## 实时运行日志面板

本版本新增前端通用组件 `RealtimeLogPanelComponent`，用于在聊天框之外实时展示 Agent Runtime 日志。它会根据当前会话的 invocation id 轮询已有接口：

```text
GET /api/runtime/traces/{invocation_id}?limit=300
```

展示内容包括 Agent 生命周期、模型调用、Tool 绑定/调用/返回、Skill 事件、错误和警告。对应设计文档见：

```text
docs/realtime-log-panel.md
skills/ui-realtime-log-panel/SKILL.md
```

第一版使用轮询复用 runtime trace，不新增 WebSocket/SSE 协议；后续可升级为 run_sse 同步推送标准 `runtime.log` 事件。


## runtime.log SSE 标准事件

`run_sse` 现在可以把关键 runtime trace 事件同步推送为标准 `runtime.log` 数据帧，前端会把它们放进实时运行日志面板，而不是混入聊天正文。

设计文档见：

```text
docs/runtime-log-sse.md
```
