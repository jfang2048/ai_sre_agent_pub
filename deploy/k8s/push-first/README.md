# Push-First Kubernetes Deployment / Kubernetes 部署指南

This folder contains baseline manifests for the push-first architecture.

本目录包含 Push-first 架构的 Kubernetes 基础清单。

## Components / 组件说明

- `sre-controller`: Control plane (HTTP API/UI + gRPC ingest + `/metrics`) / 控制平面（HTTP API/UI + gRPC 采集接入 + `/metrics`）
- `sre-collector`: Node daemon (host/process/log/GPU collection + push) / 节点守护进程（主机/进程/日志/GPU 采集 + 推送）

## Runtime operations / 运行时特性

### Supported runtime configurations / 支持的运行时配置

- `--level` is supported by `sre-collector`, so level overrides in daemonset args are valid / `--level` 参数支持 collector 动态调整采集深度（daemonset args 中可覆盖）
- Collector config can be reloaded at runtime with `SIGHUP` (e.g. `kubectl exec ... -- kill -HUP 1`) / 配置文件支持运行时重载：发送 `SIGHUP` 信号
- TLS cert rotation is supported through config reload (`transport.tls.*` fields in `configs/collector.yaml`) / TLS 证书轮换：通过配置重载实现
- Controller uses a read-only service account for Kubernetes discovery (`nodes`, `pods`, `namespaces`) / Controller 使用只读服务账号进行 Kubernetes 发现（`nodes`、`pods`、`namespaces`）

### Runtime reload example / 运行时重载示例

```bash
# Reload collector config (e.g. after updating TLS certificates)
# 重载 collector 配置（如更新 TLS 证书后）
kubectl exec -n sre-agent <collector-pod> -- kill -HUP 1
```

## Build images / 构建镜像

### Local build / 本地构建

```bash
# Build controller image / 构建 controller 镜像
docker build -f deploy/docker/Dockerfile --target controller -t sre-agent/sre-controller:latest .

# Build collector image / 构建 collector 镜像
docker build -f deploy/docker/Dockerfile --target collector -t sre-agent/sre-collector:latest .
```

### Push to registry / 推送到镜像仓库

```bash
# Tag and push (replace with your registry)
# 推送示例（替换为你的仓库地址）
docker tag sre-agent/sre-controller:latest your-registry/sre-controller:v0.2
docker tag sre-agent/sre-collector:latest your-registry/sre-collector:v0.2

docker push your-registry/sre-controller:v0.2
docker push your-registry/sre-collector:v0.2
```

## Apply manifests / 部署清单

### Sequential deployment / 顺序部署

```bash
# 1. Create namespace / 创建命名空间
kubectl apply -f deploy/k8s/push-first/namespace.yaml

# 2. Apply read-only RBAC / 应用只读 RBAC
kubectl apply -f deploy/k8s/push-first/rbac-readonly.yaml

# 3. Deploy controller / 部署 controller
kubectl apply -f deploy/k8s/push-first/controller.yaml

# 4. Deploy collector daemonset / 部署 collector daemonset
kubectl apply -f deploy/k8s/push-first/collector-daemonset.yaml
```

### One-line deployment / 一键部署

```bash
kubectl apply -f deploy/k8s/push-first/
```

### GitOps/Kustomize deployment / GitOps/Kustomize 部署

```bash
kubectl apply -k deploy/k8s/push-first
```

`kustomization.yaml` in this directory is the recommended base for GitOps controllers.
  
本目录中的 `kustomization.yaml` 是推荐的 GitOps 基线入口。

## Verify / 验证部署

```bash
# Check controller status / 检查 controller 状态
kubectl -n sre-agent rollout status deploy/sre-controller

# Check collector status / 检查 collector 状态
kubectl -n sre-agent rollout status ds/sre-collector

# View pod status / 查看 pod 状态
kubectl -n sre-agent get pods

# Port forward for testing / 端口转发测试
kubectl -n sre-agent port-forward svc/sre-controller 8080:8080

# Test APIs / 测试 API
curl -s http://127.0.0.1:8080/api/v1/fleet | head
curl -s http://127.0.0.1:8080/api/v1/top/programs?limit=20 | head
curl -s http://127.0.0.1:8080/healthz
```

## GPU notes / GPU 支持说明

### Prerequisites / 前置条件

- GPU metrics require `nvidia-smi` availability in collector runtime / GPU 指标采集需要 `nvidia-smi` 在 collector 运行时可用
- NVIDIA runtime configuration is cluster-specific (GPU Operator/device plugin/runtimeClass) / NVIDIA 运行时配置与集群相关

### Daemonset GPU configuration / Daemonset GPU 配置

The daemonset sets common runtime env hints / 清单中已设置通用的运行时环境变量：

- `NVIDIA_VISIBLE_DEVICES=all` - Expose all GPU devices / 暴露所有 GPU 设备
- `NVIDIA_DRIVER_CAPABILITIES=utility,compute` - Enable driver capabilities / 启用驱动能力

### Troubleshooting / 故障排查

```bash
# Check if GPU is accessible / 检查 GPU 是否可访问
kubectl -n sre-agent exec -it <collector-pod> -- nvidia-smi

# View GPU-related errors in collector logs / 查看 collector 日志中的 GPU 相关错误
kubectl -n sre-agent logs <collector-pod> | grep -i gpu
```

## Configuration / 配置管理

### ConfigMap mount / ConfigMap 挂载

```yaml
# controller.yaml example / 示例
volumeMounts:
  - name: config
    mountPath: /etc/sre-controller
volumes:
  - name: config
    configMap:
      name: sre-controller-config
```

### Secret mount (sensitive config) / Secret 挂载（敏感配置）

```yaml
# For API keys, TLS certificates, etc.
# 用于 API Key、TLS 证书等
volumeMounts:
  - name: secrets
    mountPath: /etc/sre-controller/secrets
    readOnly: true
volumes:
  - name: secrets
    secret:
      secretName: sre-controller-secrets
```

## Upgrade and Rollback / 升级与回滚

### Rolling upgrade / 滚动升级

```bash
# Update image version / 更新镜像版本
kubectl set image deployment/sre-controller \
  sre-controller=your-registry/sre-controller:v0.2.1 \
  -n sre-agent

kubectl set image daemonset/sre-collector \
  sre-collector=your-registry/sre-collector:v0.2.1 \
  -n sre-agent
```

### Rollback / 回滚

```bash
# View rollout history / 查看 rollout 历史
kubectl -n sre-agent rollout history deployment/sre-controller

# Rollback to previous version / 回滚到上一版本
kubectl -n sre-agent rollout undo deployment/sre-controller

# Rollback to specific revision / 回滚到指定版本
kubectl -n sre-agent rollout undo deployment/sre-controller --to-revision=2
```
