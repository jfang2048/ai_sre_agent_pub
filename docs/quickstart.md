# Quickstart (v0.7)

Use the dedicated usage guide:

- [Operations Usage](operations/usage.md): single-node and separated multi-machine workflows

## 中文说明

- 这个文件只保留“最短可启动路径”，目标是先把 controller、collector、UI 和基础数据链路跑起来，而不是在这里展开所有部署变量。
- 只写三步，是因为第一次 bring-up 最重要的是确认镜像能构建、容器能启动、页面和健康检查能返回；更接近真实权限边界的 `host-observer`、split deployment、TSDB 等路径应回到 [`operations/usage.md`](operations/usage.md)。
- 如果你现在关心的是“先看到系统活着”，这份 quickstart 就够；如果你关心“为什么这么配、生产怎么跑、退化时会怎样”，就继续看 usage 和 architecture。

Shortest local bring-up:

```bash
cp .env.example .env
make container-build
make container-up
```

Open `http://127.0.0.1:8080/`.

补充原因:

- `cp .env.example .env` 放在第一步，是为了把运行参数显式落到文件里，避免后续行为被你当前 shell 环境里偶然的变量影响。
- `make container-build` 先执行，是为了确保 controller、collector 和相关 runtime 产物来自同一份工作区状态，避免版本错位。
- `make container-up` 是最低阻力路径；如果你要验证更真实的 host 观测能力，后续应该切到 usage 文档里的 `host-observer` 启动方式。
