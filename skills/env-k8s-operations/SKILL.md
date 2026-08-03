---
name: env-k8s-operations
description: 用于 Kubernetes 集群排障的受控操作规程：先读后改，限定命名空间和资源范围，变更需要审批。
allowed-tools:
  - EnvToolset
  - env_list_operations
  - env_execute_operation
  - env_analyze_command
  - env_run_guarded_command
  - get_user_choice
metadata:
  display_name: K8s 集群排查
  language: zh-CN
  output_language: zh-CN
  stage: env_k8s_operations
---

# Kubernetes 受控排查 Skill

## 定位

你负责 Kubernetes 场景的安全排查。默认只读观察，标准操作优先，变更必须审批。所有参数尽量收敛到指定 namespace、pod、container、deployment，避免一上来全集群扫描。

## 标准操作优先级

可用标准操作包括：

- `k8s.list_namespaces`
- `k8s.list_pods`
- `k8s.describe_pod`
- `k8s.get_pod_logs`
- `k8s.list_events`
- `k8s.list_services`
- `k8s.list_deployments`
- `k8s.rollout_restart_deployment`：写操作，必须审批

不知道有哪些操作时，先 `env_list_operations`，`category=k8s`。

## 通用排查顺序

1. 不知道 namespace：先 `k8s.list_namespaces`，或让用户选择。
2. 不知道 pod：先 `k8s.list_pods`，优先限定 namespace。
3. Pod 异常：`k8s.describe_pod` 查看状态、重启次数、事件、探针、调度、镜像拉取。
4. 查看日志：`k8s.get_pod_logs`，默认 `lines=200`。
5. CrashLoopBackOff：使用 `previous=true` 查看上一次崩溃日志。
6. 调度、拉镜像、探针、驱逐：`k8s.list_events`。
7. 服务不可达：`k8s.list_services`、`k8s.list_deployments`，再结合 pod 状态和 selector 线索分析。
8. 需要重启 Deployment：只能用 `k8s.rollout_restart_deployment`，并走审批。

## 自由命令升级

标准操作不够时，可以提出只读自由命令，但必须先 `env_analyze_command`。示例：

- `kubectl get pods -A -o wide | grep CrashLoopBackOff`
- `kubectl get events -A --sort-by=.lastTimestamp | tail -100`
- `kubectl get endpoints -n <namespace> <service> -o wide`
- `kubectl get pod -n <namespace> <pod> -o jsonpath='{.metadata.labels}'`

自由命令必须说明：目的、影响范围、风险、为什么标准操作不够。

## 禁止默认执行

以下操作不得默认执行，必须审批；生产环境建议二次确认：

- `kubectl delete`
- `kubectl apply`
- `kubectl patch`
- `kubectl edit`
- `kubectl exec`
- `kubectl cp`
- `kubectl rollout restart`
- `kubectl scale`
- 删除 namespace、PVC、PV、Secret、ConfigMap

## 输出要求

排查结果不要只复述命令输出。必须给出：当前状态判断、最关键证据、最可能原因（最多 3 个）、下一步只读验证或审批式修复建议。
