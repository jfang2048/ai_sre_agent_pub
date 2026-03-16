# 部署

English version: [docs/en/15-deployment.md](../en/15-deployment.md)

这个仓库现在更明确地区分了 node-local 采集和中心化分析。

> 仓库仍然没有公开 release pipeline，也没有发布预构建镜像。当前支持的路径仍然是：从当前 checkout 构建，本地验证，再分阶段 rollout。

为了理解这种部署拆分背后的运行时行为，建议把下面两页一起看：

- [采集队列与压缩](06-collector-queue-and-compaction.md)：节点侧缓冲、抑制、慢接收端行为
- [控制平面分析](07-control-plane-analysis.md)：控制器侧趋势分析、弱信号融合、TSDB fallback、建议生成

## 推荐部署形态

| 模式 | 适合什么场景 | 主要资产 |
| --- | --- | --- |
| `local-dev` | 你要在一个 checkout 里改代码和调试 | [`scripts/run-local.sh`](../../scripts/run-local.sh)、[`configs/controller.yaml`](../../configs/controller.yaml)、[`configs/collector.yaml`](../../configs/collector.yaml) |
| `standalone` | 你要一个中心 controller 和少量远端 collector，但不使用 Kubernetes | [`deploy/docker/`](../../deploy/docker/)、[`deploy/systemd/`](../../deploy/systemd/) |
| `cluster-lite` | 你想要最快的 Kubernetes 部署路径 | [`deploy/k8s/push-first/`](../../deploy/k8s/push-first/)、[`deploy/charts/sre-agent/`](../../deploy/charts/sre-agent/) 默认值 |
| `distributed` | 你要多副本 controller 和共享后端 | Helm 加 `controller.ha.enabled=true` 和外部后端配置 |

## 运行边界

| 平面 | 运行什么 | 状态放在哪里 |
| --- | --- | --- |
| 数据面 | collector、probe-core、eBPF、本地 spool | 节点本地 spool 和运行时缓存 |
| 控制面 | controller ingest、API、UI、workflow、RAG、可选 TSDB | controller 缓存、可选嵌入式 ingest DB、可选外部 TSDB/向量后端 |

对应代码：

- [`../../backend/internal/collector/deployment.go`](../../backend/internal/collector/deployment.go)
- [`../../backend/internal/controller/deployment.go`](../../backend/internal/controller/deployment.go)

## 这次为集群部署做了什么

仓库现在比之前更少依赖单机假设：

- controller 和 collector 都理解 `deployment.mode`、`deployment.cluster_name`、`deployment.data_root`
- 非本地模式只会重写内置默认式路径到 `/var/lib/ai-sre-agent/...`
- controller 现在额外暴露 `/readyz`
- collector metrics server 现在额外暴露 `/readyz`
- `/api/v1/status` 现在包含 `deployment` 字段
- controller RAG 配置现在支持 YAML 字段：
  - `rag_vector_backend`
  - `rag_vector_endpoint`
  - `rag_vector_collection`
  - `rag_vector_database`
  - `rag_vector_token`
  - `rag_vector_timeout`
- 原始 Kubernetes manifests 现在通过 ConfigMap 挂载配置，而不是只依赖镜像内默认配置
- Helm chart 现在也挂载 ConfigMap，并暴露 deployment mode、cluster name、ingress、HPA、外部向量后端参数

## 配置里的部署模式

相关文件：

- [`../../configs/controller.yaml`](../../configs/controller.yaml)
- [`../../configs/collector.yaml`](../../configs/collector.yaml)
- [`../../configs/container/controller.yaml`](../../configs/container/controller.yaml)
- [`../../configs/container/collector.yaml`](../../configs/container/collector.yaml)

controller 示例：

```yaml
deployment:
  mode: "cluster-lite"      # local-dev | standalone | cluster-lite | distributed
  cluster_name: "prod-eu1"
  data_root: "/var/lib/ai-sre-agent"
  external_url: "https://ai-sre-agent.example.com"
```

collector 示例：

```yaml
deployment:
  mode: "cluster-lite"
  cluster_name: "prod-eu1"
  data_root: "/var/lib/ai-sre-agent"
```

在非本地模式下，加载器会把默认式路径迁移成：

- collector spool -> `/var/lib/ai-sre-agent/collector/data/spool`
- collector eBPF socket -> `/var/lib/ai-sre-agent/collector/data/run/sre_collector_ebpf.sock`
- controller web path -> `/var/lib/ai-sre-agent/controller/web`
- controller ingest persistence -> `/var/lib/ai-sre-agent/controller/data/ingest/store.db`
- controller RAG index -> `/var/lib/ai-sre-agent/controller/data/agent/rag/index.json`

显式自定义路径仍然优先。

## 本地开发

```bash
cp .env.example .env
make container-build
make container-up-host-observer
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
```

适合：

- 在源码 checkout 里调试
- 一台机器同时跑 controller 和 collector
- 暂时不需要集群调度或共享后端

## Cluster-Lite Kubernetes 路径

现在的快速集群路径是：

- 一个 controller `Deployment`
- 一个 collector `DaemonSet`
- controller 和 collector 都通过 ConfigMap 注入配置
- 可选的 controller 静态 inventory 文件也通过 ConfigMap 注入

文件：

- [`../../deploy/k8s/push-first/controller.yaml`](../../deploy/k8s/push-first/controller.yaml)
- [`../../deploy/k8s/push-first/controller-configmap.yaml`](../../deploy/k8s/push-first/controller-configmap.yaml)
- [`../../deploy/k8s/push-first/controller-targets-configmap.yaml`](../../deploy/k8s/push-first/controller-targets-configmap.yaml)
- [`../../deploy/k8s/push-first/collector-daemonset.yaml`](../../deploy/k8s/push-first/collector-daemonset.yaml)
- [`../../deploy/k8s/push-first/collector-configmap.yaml`](../../deploy/k8s/push-first/collector-configmap.yaml)

这条路径里的 collector 身份不是手写的，而是直接取自节点：

- `SRE_COLLECTOR_ID <- spec.nodeName`
- `SRE_COLLECTOR_HOSTNAME <- spec.nodeName`

这就是 cluster-lite 路径避免“每个节点维护一份独立配置文件漂移”的方式。

应用：

```bash
kubectl apply -k deploy/k8s/push-first
```

这个路径默认假设：

- controller 是中心化的，但不是 HA
- collector 每节点一个
- 本地文件型 RAG 索引可以接受
- node-local spool 可以接受
- collector pod 允许 host-observer 权限

## Helm 路径

当你需要参数化集群部署时，[`../../deploy/charts/sre-agent/`](../../deploy/charts/sre-agent/) 现在是更好的入口。

关键 values：

| 值 | 用途 |
| --- | --- |
| `global.deploymentMode` | 两个组件共享的默认部署模式 |
| `global.clusterName` | 集群身份 |
| `controller.deploymentMode`、`collector.deploymentMode` | 单组件覆盖 |
| `controller.externalURL` | 外部 UI/API 地址 |
| `controller.rag.vectorBackend` | `local` 或 `milvus` |
| `controller.rag.vectorEndpoint` | 外部向量后端地址 |
| `controller.rag.vectorCollection` | collection 名称 |
| `controller.rag.vectorTokenSecretName` | 向量后端认证 Secret 名称 |
| `controller.rag.vectorTokenSecretKey` | 注入 `SRE_AGENT_RAG_VECTOR_TOKEN` 时使用的 Secret key |
| `controller.ingress.*` | ingress 模板 |
| `controller.autoscaling.*` | 非 HA controller 的 HPA 模板 |
| `controller.staticTargets` | 静态 inventory 文件内容 |

关键模板：

- [`../../deploy/charts/sre-agent/templates/controller-configmap.yaml`](../../deploy/charts/sre-agent/templates/controller-configmap.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-rag-secret.yaml`](../../deploy/charts/sre-agent/templates/controller-rag-secret.yaml)
- [`../../deploy/charts/sre-agent/templates/collector-configmap.yaml`](../../deploy/charts/sre-agent/templates/collector-configmap.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-deployment.yaml`](../../deploy/charts/sre-agent/templates/controller-deployment.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-statefulset.yaml`](../../deploy/charts/sre-agent/templates/controller-statefulset.yaml)
- [`../../deploy/charts/sre-agent/templates/collector-daemonset.yaml`](../../deploy/charts/sre-agent/templates/collector-daemonset.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-ingress.yaml`](../../deploy/charts/sre-agent/templates/controller-ingress.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-hpa.yaml`](../../deploy/charts/sre-agent/templates/controller-hpa.yaml)

示例 values 文件：

- [`../../deploy/charts/sre-agent/examples/cluster-lite-values.yaml`](../../deploy/charts/sre-agent/examples/cluster-lite-values.yaml)
- [`../../deploy/charts/sre-agent/examples/distributed-values.yaml`](../../deploy/charts/sre-agent/examples/distributed-values.yaml)

## Distributed 参考模式

当你要更接近生产式 split 部署时：

- 每个节点一个 collector `DaemonSet`
- controller 通过 Helm 的 StatefulSet 路径多副本部署
- 通过 `controller.ha.enabled=true` 启用 HA
- 可选外部向量后端，例如 Milvus
- 可选外部 TSDB 用于更长历史窗口

示例 values：

```yaml
global:
  deploymentMode: distributed
  clusterName: prod-eu1

controller:
  replicas: 3
  ha:
    enabled: true
    backend: etcd
    etcdEndpoints:
      - http://etcd-0.etcd.sre.svc.cluster.local:2379
      - http://etcd-1.etcd.sre.svc.cluster.local:2379
      - http://etcd-2.etcd.sre.svc.cluster.local:2379
  rag:
    vectorBackend: milvus
    vectorEndpoint: http://milvus.monitoring.svc.cluster.local:19530
    vectorCollection: ai_sre_agent_knowledge
    vectorDatabase: ai_sre
    vectorTokenSecretName: milvus-rag-token
    vectorTokenSecretKey: token
```

这仍然是增量式改进，而不是完全无状态 controller：

- ingest 热状态仍然在进程内
- `vectorBackend=local` 时仍然会使用本地文件索引
- report 和 workflow 状态还没有外部化到共享数据库

## RAG 部署选择

现在有两种更现实的检索部署方式：

| 方式 | 配置 | 适用场景 |
| --- | --- | --- |
| 本地文件索引 | `rag_vector_backend: local` 加 `rag_index_path` | 单 controller 或 cluster-lite |
| 外部向量后端 | `rag_vector_backend: milvus` 加 endpoint/collection/database，并通过 Secret 注入 `SRE_AGENT_RAG_VECTOR_TOKEN` | 分布式 controller 部署 |

代码路径：

- [`../../backend/internal/controller/rag/retriever.go`](../../backend/internal/controller/rag/retriever.go)
- [`../../backend/internal/controller/rag/service.go`](../../backend/internal/controller/rag/service.go)
- [`../../backend/internal/controller/controller.go`](../../backend/internal/controller/controller.go)
- [`../../backend/internal/controller/agent/engine.go`](../../backend/internal/controller/agent/engine.go)
- [`../../backend/internal/controller/agentcore/agent.go`](../../backend/internal/controller/agentcore/agent.go)

回退行为：

- 本地索引损坏时，会被 quarantine，然后按 `rag_rebuild_policy` 重建或保持禁用
- 向量后端不可用时，controller 会回退到确定性行为，而不是直接崩溃
- 检索置信度过低时，controller 会在 prompt 组装前抑制这批检索证据

## 健康、就绪与状态

rollout 时优先看这些端点：

| 组件 | 存活 | 就绪 | 更详细状态 |
| --- | --- | --- | --- |
| controller | `/healthz` | `/readyz` | `/api/v1/status` |
| collector | `/healthz` | `/readyz` | `/metrics` |

关键状态字段：

- `/api/v1/status.deployment.mode`
- `/api/v1/status.deployment.cluster_name`
- `/api/v1/status.deployment.data_root`
- `/api/v1/status.ha`

## 权限与降级模式

collector pod 仍然是系统里更需要权限的一侧。

相关部署假设在这里：

- [`../../deploy/k8s/push-first/collector-daemonset.yaml`](../../deploy/k8s/push-first/collector-daemonset.yaml)
- [`../../deploy/charts/sre-agent/templates/collector-daemonset.yaml`](../../deploy/charts/sre-agent/templates/collector-daemonset.yaml)

如果这些假设不满足：

- eBPF 会降级
- probe-core 可以 fallback
- controller 侧推理仍然可运行，但证据质量下降
- telemetry 质量不足时，RAG 和 LLM 会被安全跳过

## Rollout 检查表

1. 从当前 checkout 构建镜像。
2. 跑目标测试：

```bash
cd backend
go test ./internal/collector ./internal/controller ./internal/controller/agent ./internal/controller/agentcore
```

3. 本地验证：

```bash
make container-smoke
make rag-status
```

4. 如果使用 Kubernetes，确认：

- controller `/healthz` 和 `/readyz`
- collector `/healthz` 和 `/readyz`
- `/api/v1/status` 返回预期的 `deployment` 字段
- fleet 节点带有正确的 `cluster` 和 `deployment_mode` 标签

5. 先灰度到一个节点或一个小集群分区。

## 仍然存在的限制

- 仓库仍然没有公开镜像分发或 release 服务。
- controller 还不是完全无状态。
- Helm 模板这次已经更新，但当前工作空间里没有 `helm` 二进制，因此没有做 live render 校验。
- 分布式 RAG 的改进目前主要是“更清晰的外部后端配置路径”，还不是一套新的分布式检索框架。
