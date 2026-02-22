# Contributing to AI SRE Agent / 贡献指南

AI SRE Agent is a push-first Linux observability system (`sre-collector` + `sre-controller`).

AI SRE Agent 是一个基于 Push-first 模式的 Linux 可观测性系统（`sre-collector` + `sre-controller`）。

## 1. Development Flow / 开发流程

### Code submission flow / 代码提交流程

1. Create a feature branch from `main` / 从 `main` 分支创建功能分支
2. Keep commits atomic (one behavior change per commit) / 保持提交原子化（每次提交只包含一个行为变更）
3. Rebase on latest `main` before opening a PR / 打开 PR 前先 rebase 到最新的 `main`
4. Open a PR with / 提交 PR 时包含：
   - behavior summary / 行为变更摘要
   - test evidence / 测试证据
   - doc updates for changed APIs/config/operations / 相关 API/配置/运维文档的更新

### Branch naming conventions / 分支命名规范

- `feat/<topic>` - New features / 新功能
- `fix/<topic>` - Bug fixes / 问题修复
- `docs/<topic>` - Documentation updates / 文档更新
- `chore/<topic>` - Chores and build-related / 杂项/构建相关

## 2. Repository Layout / 代码仓库结构

| Path | Purpose | 用途 |
|---|---|---|
| `backend/cmd/collector` | Collector entrypoint (`sre-collector`) | Collector 入口 |
| `backend/cmd/controller` | Controller entrypoint (`sre-controller`) | Controller 入口 |
| `backend/internal/collector` | Collector runtime (spool, transport, config) | Collector 运行时（队列、传输、配置） |
| `backend/internal/probe` | Host telemetry primitives (`/proc`, kernel, GPU, eBPF) | 主机遥测原语 (`/proc`、kernel、GPU、eBPF) |
| `backend/internal/controller` | Ingest store, APIs, rankings, analysis | 数据存储、API、排名、分析 |
| `frontend/` | React/Vite dashboard source | React/Vite 前端仪表盘源码 |
| `python/` | Python SRE agent modules | Python SRE agent 模块 |
| `cpp/` | Native metrics helpers | 原生指标助手 |
| `docs/` | Design, operations, references | 设计文档、运维手册、参考文档 |

## 3. Tooling Prerequisites / 工具要求

- Go `1.25+`
- Node.js `18+` (for frontend changes / 前端变更时需要)
- Python `3.10+` (for python agent changes / Python agent 变更时需要)
- `clang-tidy` (for C++ checks / C++ 代码检查)
- `markdownlint-cli` (for Markdown checks / Markdown 格式检查)

## 4. Required Checks Before PR / PR 前必做检查

Run checks for the layers you touched / 根据你修改的层级运行相应检查：

```bash
# Go code / Go 代码
make fmt-check   # Format check / 格式检查
make vet         # Static analysis / 静态分析
make test-stability  # Layered stability tests / 分层稳定性测试

# Frontend code / 前端代码
npm -C frontend test

# Python code / Python 代码
python3 -m unittest discover -s tests/python -p 'test_*.py'
```

Recommended additional checks / 推荐额外检查：

```bash
# Go unit test coverage / Go 单元测试覆盖率
go -C backend test -cover ./...

# Frontend lint / 前端代码检查
npm -C frontend run lint

# C++ code check / C++ 代码检查
clang-tidy cpp/proc_metrics/main.cpp -- -std=c++17

# Markdown format check / Markdown 格式检查
markdownlint README.md docs/**/*.md CONTRIBUTING.md
```

## 5. Language Standards / 编码规范

### Go

- Always pass `context.Context` through call chains / 始终在调用链中传递 `context.Context`
- Return wrapped errors (`fmt.Errorf("...: %w", err)`) and use `errors.Is`/`errors.As` for handling / 返回包装的错误，使用 `errors.Is`/`errors.As` 处理
- Prefer table-driven tests for branch-heavy logic / 优先使用表驱动测试处理分支繁多的逻辑
- Keep functions short and single-purpose; split long loops into helpers / 保持函数简短、单一职责；将长循环拆分为辅助函数

### TypeScript

- Keep `strict` mode clean / 保持 `strict` 模式无警告
- Avoid `any`; model unknown payloads with narrow types and guards / 避免使用 `any`；用精确的类型和类型守卫来建模未知负载
- Use async/await and handle rejected promises explicitly / 使用 async/await，显式处理拒绝的 Promise

### Python

- Add type hints for public functions / 为公共函数添加类型提示
- Validate parameters early and raise explicit exceptions / 尽早校验参数，抛出明确的异常
- Prefer context managers for resources and side-effecting operations / 对于资源和副作用操作优先使用上下文管理器

### C++

- Prefer RAII and smart ownership (`std::unique_ptr` where ownership is dynamic) / 优先使用 RAII 和智能所有权（动态所有权场景使用 `std::unique_ptr`）
- Keep const-correct APIs and bounds-aware parsing / 保持 const 正确的 API 和边界感知的解析
- Fail with non-zero exit codes on unrecoverable input/runtime errors / 在不可恢复的输入/运行时错误时以非零退出码失败

## 6. Documentation Standards / 文档规范

- One idea per section / 每个段落只表达一个核心观点
- Use active voice and explicit subject/object / 使用主动语态，明确主语和宾语
- Include runnable command blocks for setup/operations / 包含可运行的命令块（用于配置/运维）
- Cross-link related docs when behavior spans files / 当行为涉及多个文件时，添加相关文档交叉引用

When behavior changes, update docs in the same PR / 当行为变更时，在同一 PR 中更新相关文档：

- API behavior: `docs/reference/api.md`
- Metric semantics: `docs/reference/metrics.md`
- Operational flow: `docs/operations/*.md`
- Architecture boundaries: `docs/design/*.md`

## 7. AGENT Contributions / AGENT 模块贡献

When touching AGENT (`backend/internal/agent`, `/api/v1/agent/*`, playbooks) / 修改 AGENT 相关代码时：

- Keep AGENT isolated from ingest hot paths; AGENT failures must not block telemetry ingest / 保持 AGENT 与采集热路径隔离；AGENT 失败绝不能阻塞遥测数据采集
- Add/maintain idempotency for action execution paths / 为动作执行路径添加/维持幂等性
- Keep `SRE_AGENT_DRY_RUN=1` as the safest default in examples and tests / 在示例和测试中保持 `SRE_AGENT_DRY_RUN=1` 作为最安全的默认值
- Add or update table-driven tests for prompt schema and action guardrails / 为 prompt schema 和动作防护添加表驱动测试
- Update both when changing action contracts or LLM payload shape / 当更改动作契约或 LLM 负载结构时，同步更新：
  - `configs/agent_playbooks.yaml`
  - `docs/reference/llm_schema.md`

## 8. PR Checklist / PR 检查清单

- [ ] Code compiles for changed components / 代码编译通过
- [ ] Relevant tests pass / 相关测试通过
- [ ] Lint/format checks pass / Lint/格式检查通过
- [ ] Docs match the shipped behavior / 文档与实际行为一致
- [ ] No credentials, tokens, or private keys are committed / 未提交任何凭证、令牌或私钥

## License / 开源许可

By contributing, you agree your work is licensed under `GPL-3.0` / 贡献即表示你同意你的工作在 `GPL-3.0` 许可下发布。
