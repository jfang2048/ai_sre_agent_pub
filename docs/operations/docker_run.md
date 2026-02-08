# Docker Run Guide / Docker 运行指南

Run `sre-controller` and `sre-collector` on a laptop using plain Docker commands (no Compose).

使用纯 `docker build` + `docker run` 在笔记本上启动 `sre-controller` 与 `sre-collector`（不依赖 Compose）。

## Chinese Quick Start / 中文速用

### Prerequisites / 前置条件

- Docker installed and running / 已安装并启动 Docker
- Access to Docker socket / 当前用户可访问 Docker socket（Linux 可加入 `docker` 组，或使用 `sudo`）
- Free host ports / 本地端口可用：
  - `8080`: HTTP / UI / API
  - `9090`: gRPC ingest / gRPC 采集

### 1-Minute Start / 1 分钟启动

```bash
# Build images and start both containers / 构建镜像并启动双容器
./scripts/docker-run-stack.sh

# Health checks / 健康检查
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/fleet

# Stop / 停止
./scripts/docker-stop-stack.sh
```

### Script Description / 脚本说明

**`scripts/docker-build.sh`**
- Builds both images (collector + controller) / 构建两个镜像（collector + controller）
- Usage / 用法：`./scripts/docker-build.sh [--no-cache]`

**`scripts/docker-run-stack.sh`**
- Creates network + volumes, starts controller, waits for readiness, then starts collector / 创建网络与卷，先起 controller，健康后起 collector
- Usage / 用法：`./scripts/docker-run-stack.sh [--skip-build]`

**`scripts/docker-stop-stack.sh`**
- Stops and removes containers / 停止并删除容器
- Usage / 用法：`./scripts/docker-stop-stack.sh [--prune]`

### Common Environment Variables / 常用环境变量

| Variable / 变量 | Default / 默认值 | Purpose / 作用 |
|---|---|---|
| `COLLECTOR_IMAGE` | `sre-collector:latest` | Collector image tag / collector 镜像标签 |
| `CONTROLLER_IMAGE` | `sre-controller:latest` | Controller image tag / controller 镜像标签 |
| `SRE_DOCKER_NETWORK` | `sre-agent-net` | Docker network name / Docker 网络名 |
| `SRE_CONTROLLER_CONTAINER` | `sre-controller` | Controller container name / controller 容器名 |
| `SRE_COLLECTOR_CONTAINER` | `sre-collector` | Collector container name / collector 容器名 |
| `SRE_CONTROLLER_VOLUME` | `sre-controller-data` | Controller data volume / controller 数据卷 |
| `SRE_COLLECTOR_VOLUME` | `sre-collector-data` | Collector data volume / collector 数据卷 |
| `SRE_CONTROLLER_HTTP_PORT` | `8080` | Mapped HTTP port / 映射到主机的 HTTP 端口 |
| `SRE_CONTROLLER_GRPC_PORT` | `9090` | Mapped gRPC port / 映射到主机的 gRPC 端口 |
| `SRE_COLLECTOR_LEVEL` | `5` | Collector collection level / collector 采集级别 |

### Common Examples / 常见示例

**Custom ports / 自定义端口：**

```bash
SRE_CONTROLLER_HTTP_PORT=18080 \
SRE_CONTROLLER_GRPC_PORT=19090 \
./scripts/docker-run-stack.sh
```

**Skip build, use existing images / 跳过构建，直接用已有镜像：**

```bash
./scripts/docker-run-stack.sh --skip-build
```

**Full cleanup (including network and volumes) / 完全清理（含网络与卷）：**

```bash
./scripts/docker-stop-stack.sh --prune
```

### Troubleshooting Tips / 排查建议

- **Error: `permission denied while trying to connect to the docker API`**
  - User cannot access `/var/run/docker.sock` / 用户无权访问 `/var/run/docker.sock`
  - Linux fix / Linux 修复：`sudo usermod -aG docker "$USER"`，然后重新登录
- **Error: Port already in use / 报错端口占用**
  - Use custom ports / 改端口启动：`SRE_CONTROLLER_HTTP_PORT=18080 SRE_CONTROLLER_GRPC_PORT=19090 ./scripts/docker-run-stack.sh`
- **Need host-level `/proc` metrics / 需要主机级 `/proc` 指标**
  - Current scripts use laptop-safe defaults (container namespace collection) / 当前脚本默认是笔记本安全配置（容器命名空间采集）
  - For host namespace collection, manually add `--pid host`, mounts, and capabilities to collector based on your security policy / 若要主机级采集，需要按安全策略手工给 collector 增加 `--pid host`、挂载与能力配置

---

## Prerequisites

- Docker installed and running
- Access to Docker socket:
  - Linux: user in `docker` group, or run with `sudo`
- Free host ports:
  - `8080` for HTTP/UI/API
  - `9090` for gRPC ingest

## Quick Start

```bash
# Build images + start both containers
./scripts/docker-run-stack.sh

# Check service
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/fleet

# Stop containers
./scripts/docker-stop-stack.sh
```

## Script Reference / 脚本参考

### `scripts/docker-build.sh`

Builds both images from `deploy/docker/Dockerfile`:

- target `collector` -> `sre-collector:latest`
- target `controller` -> `sre-controller:latest`

Usage / 用法：

```bash
./scripts/docker-build.sh [--no-cache]
```

### `scripts/docker-run-stack.sh`

Creates network + volumes, starts controller, waits for readiness, then starts collector / 创建网络与卷，启动 controller，等待就绪后启动 collector。

Usage / 用法：

```bash
./scripts/docker-run-stack.sh [--skip-build]
```

### `scripts/docker-stop-stack.sh`

Stops and removes containers. Optional full cleanup removes network + volumes / 停止并删除容器。可选的完全清理会删除网络和卷。

Usage / 用法：

```bash
./scripts/docker-stop-stack.sh [--prune]
```

## Environment Overrides / 环境变量覆盖

| Variable | Default | Used by |
|---|---|---|
| `DOCKERFILE` | `deploy/docker/Dockerfile` | `docker-build.sh` |
| `COLLECTOR_IMAGE` | `sre-collector:latest` | build/run |
| `CONTROLLER_IMAGE` | `sre-controller:latest` | build/run |
| `SRE_DOCKER_NETWORK` | `sre-agent-net` | run/stop |
| `SRE_CONTROLLER_CONTAINER` | `sre-controller` | run/stop |
| `SRE_COLLECTOR_CONTAINER` | `sre-collector` | run/stop |
| `SRE_CONTROLLER_VOLUME` | `sre-controller-data` | run/stop |
| `SRE_COLLECTOR_VOLUME` | `sre-collector-data` | run/stop |
| `SRE_CONTROLLER_HTTP_PORT` | `8080` | run |
| `SRE_CONTROLLER_GRPC_PORT` | `9090` | run |
| `SRE_COLLECTOR_LEVEL` | `5` | run |

## Examples / 示例

### Use custom host ports / 使用自定义主机端口

```bash
SRE_CONTROLLER_HTTP_PORT=18080 \
SRE_CONTROLLER_GRPC_PORT=19090 \
./scripts/docker-run-stack.sh
```

### Use custom image tags / 使用自定义镜像标签

```bash
COLLECTOR_IMAGE=myrepo/sre-collector:v0.2 \
CONTROLLER_IMAGE=myrepo/sre-controller:v0.2 \
./scripts/docker-run-stack.sh --skip-build
```

### Use custom container names / 使用自定义容器名称

```bash
SRE_CONTROLLER_CONTAINER=demo-controller \
SRE_COLLECTOR_CONTAINER=demo-collector \
./scripts/docker-run-stack.sh
```

### Full cleanup (containers + network + volumes) / 完全清理

```bash
./scripts/docker-stop-stack.sh --prune
```

## Makefile Shortcuts / Makefile 快捷方式

```bash
make docker-build
make docker-run-stack
make docker-stop-stack
make docker-stop-stack PRUNE=1
```

## Logs and Debugging / 日志和调试

```bash
docker logs -f sre-controller
docker logs -f sre-collector
docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
```

If startup fails, the run script already prints recent controller logs / 如果启动失败，运行脚本会自动打印最近的 controller 日志。

## Troubleshooting / 故障排查

### `permission denied while trying to connect to the docker API`

Your user cannot access `/var/run/docker.sock` / 您的用户无法访问 `/var/run/docker.sock`。

Linux fix / Linux 修复：

```bash
sudo usermod -aG docker "$USER"
# log out/in after this / 之后需要重新登录
```

Or run scripts with `sudo` / 或使用 `sudo` 运行脚本。

### `bind: address already in use`

Port conflict on `8080` or `9090` / `8080` 或 `9090` 端口冲突。

Use custom ports / 使用自定义端口：

```bash
SRE_CONTROLLER_HTTP_PORT=18080 SRE_CONTROLLER_GRPC_PORT=19090 ./scripts/docker-run-stack.sh
```

### Need host-level `/proc` observability / 需要主机级 `/proc` 可观测性

Current scripts are laptop-safe defaults. They collect from container namespace / 当前脚本是笔记本安全的默认配置。它们从容器命名空间采集。

For host namespace collection, run collector manually with additional Docker runtime flags (`--pid host`, mounts/capabilities) based on your security policy / 对于主机命名空间采集，根据安全策略手动运行 collector 并附加 Docker 运行时标志（`--pid host`、挂载/能力）。
