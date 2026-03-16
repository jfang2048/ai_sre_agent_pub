# Tests

## 中文说明

- `tests/` 不是单一测试层，而是按验证目标拆分的集合。这样做的原因是同一个改动未必需要同样重的验证路径。
- Go integration、external e2e、Python analysis/runtime、Playwright UI 分开列出，是为了让开发者先判断自己修改的是哪一层，再选择成本合适的测试。
- probe-core coverage 被明确写在正文里，是为了防止读者误以为缺少单独 C++ 测试目录就代表核心路径没被验证。真实契约是在 collector/controller 使用的 IPC 边界上。

## Layout

- `tests/integration/`: Go integration tests.
- `tests/e2e/`: external-stack end-to-end tests (may skip when prerequisites are missing).
- `tests/python/`: Python analysis/runtime tests.
- `tests/ui/`: Playwright browser smoke tests.
- `tests/fixtures/`: shared test data.

Native probe-core coverage is exercised through the Go-side IPC boundary and live-binary tests under `backend/internal/collector/probecore/`. That is the real contract the controller and collector rely on; unbuilt standalone C++ tests are intentionally not kept around.

## Main commands

```bash
# full stability workflow
make test-stability

# backend-only
make test

# Playwright UI smoke (auto-bootstraps local stack)
make test-ui
```

中文使用建议:

- 日常改动先从 `make test` 或更轻量的局部验证开始。
- 改 UI 或控制面闭环时补 `make test-ui`。
- 需要做更完整的回归时再跑 `make test-stability`，不要把最高成本流程当成每次改动的默认入口。
