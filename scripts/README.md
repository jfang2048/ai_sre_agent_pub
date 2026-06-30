# Scripts Layout

The repository keeps operational scripts grouped by responsibility instead of mixing bootstrap, publish,
and test helpers in one flat directory.

## 中文说明

- `scripts/` 的拆分重点不是目录整洁，而是把失败模式和使用场景不同的脚本隔开。bootstrap、publish、test、日常运行脚本的风险和调用频率都不同。
- 这样组织的原因是降低误用成本。开发者更容易知道“我现在是在准备环境、发布镜像、跑测试，还是直接启动运行时”，也更容易给 CI/自动化选择稳定入口。

Current groups:

- `scripts/bootstrap/`: local environment setup and optional dataset import helpers
- `scripts/publish/`: public-tree preparation and mirror publishing helpers
- `scripts/test/`: smoke and browser test entrypoints
- repository-root `scripts/*.sh`: stable day-to-day dev/build entrypoints, including role-focused controller/collector runtime and Docker helpers

The runtime scripts eventually drive the same maintained binaries and libraries described elsewhere:

- `backend/cmd/controller/main.go`
- `backend/cmd/collector/main.go`
- the Docker and Helm assets under `deploy/`

Why this split exists:

- bootstrap concerns have different failure modes than runtime/dev scripts
- publish helpers must enforce stricter filtering and file-size audits than normal development flows
- test wrappers need isolated bootstrap behavior and should stay discoverable near CI commands

中文原因补充:

- 如果把这些脚本都平铺在同一个目录里，最常见的问题不是“找不到文件”，而是误把高风险发布脚本当成普通开发脚本，或者反过来。
- 现在的分层是在用目录结构表达责任边界，这比只靠文件名约定更稳。
