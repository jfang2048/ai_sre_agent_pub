# Python Runtime Boundary (v0.8)

The repository contains an optional Python package under `python/sre_agent`.

## Support stance

- Status: `optional` and `non-hot-path`
- Intended use: experimentation, AI-side service integration, and offline or sidecar-style enrichment
- Not intended use: primary collector path, primary controller ingest path, or deterministic predictive hot path

中文说明:

- Python 运行时不是主采集链路，也不是主预测链路。
- 它存在的价值主要是给 AI 侧实验、服务桥接、以及一些不适合放到 collector/controller 热路径里的扩展提供空间。
- 生产上可以用，但前提是把它当成可选能力，而不是系统能否观测和预警的基础依赖。

## What is in the package

- `sre_agent.cli`: CLI entrypoint (`sre-agent-python`)
- `sre_agent.ai.service`: optional AI service process
- `sre_agent.bridge.server`: gRPC bridge/service scaffold
- `sre_agent.analysis.*`: forecasting/anomaly helper modules for experiments and offline analysis

## Deployment guidance

- keep it behind controller-side trust boundaries
- do not expose it directly to collectors
- prefer TLS if it is deployed across hosts
- treat it as an auxiliary component with explicit ownership and dependency tracking

## Why this boundary matters

Without this boundary, Python becomes ambiguous:

- operators do not know whether it is safe to disable
- security review does not know whether to model it as control-plane critical
- contributors keep moving deterministic logic into a runtime with a looser footprint budget

The project therefore treats Python as useful, but not foundational, for `v0.8`.
