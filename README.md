# AI SRE Agent

[![Version](https://img.shields.io/badge/version-v0.95-2ea44f?style=flat-square)](https://github.com/jfang2048/ai_sre_agent_pub/releases/tag/v0.95)
[![License](https://img.shields.io/badge/license-GPL--3.0-blue?style=flat-square)](LICENSE)
[![CI](https://github.com/jfang2048/ai_sre_agent_pub/actions/workflows/ci.yml/badge.svg?branch=v0.95)](https://github.com/jfang2048/ai_sre_agent_pub/actions/workflows/ci.yml)

[简体中文](README.zh-CN.md)

AI SRE Agent collects evidence from Linux, Kubernetes and GPU infrastructure. It helps on-call engineers investigate incidents, trace findings to evidence and prepare changes for operator review.

## Features

- **Investigate incidents.** Follow evidence from telemetry and logs to a root-cause hypothesis, remediation proposal and verification record. [Project handbook](docs/operator-guide.md#investigate-an-incident)
- **Monitor inference services.** View vLLM queues, first-token delay, output timing and throughput with same-node GPU observations. [Inference monitoring](docs/operator-guide.md#llm-serving-and-gpu-monitoring)
- **Audit deployment evidence.** Import SARIF scan reports and Kubernetes audit logs. Review related events, unusual denial rates and evidence links in **Security Dashboard → DevSecOps Audit**. [DevSecOps audit](docs/operator-guide.md#devsecops-audit-workspace)

## Architecture

Probes sample the host. Collectors buffer and push batches. The controller applies policy and records each investigation. The UI and CLI tools read through HTTP APIs and files. Versioned records carry evidence IDs from one stage to the next. [Runtime architecture](docs/operator-guide.md#unix-composition)

## Quick start

On Linux, install Git, GNU Make, Bash, curl, Go 1.26.8, and Node.js 22.12 or later in the 22.x series with npm. [Native probe requirements](docs/operator-guide.md#install-the-build-prerequisites)

```bash
git clone --branch v0.95 https://github.com/jfang2048/ai_sre_agent_pub.git
cd ai_sre_agent_pub
npm -C frontend ci
make run-both
```

Open <http://127.0.0.1:8080/> or use the address printed at startup. To check controller readiness, run this command in a second terminal. Expect `OK`.

```bash
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
```

Check the collector and latest sample in the dashboard. Press `Ctrl+C` in the startup terminal to stop the stack. [Local configuration and demo](docs/operator-guide.md#start-the-local-stack)

## Documentation

| I want to | Start here |
| --- | --- |
| Deploy a controller and collectors | [Deployment](deploy/README.md) · [Telemetry durability](deploy/telemetry-durability.md) |
| Review the rules for production changes | [Tool contracts and execution policy](docs/operator-guide.md#tool-contracts-and-execution-policy) |
| Practice a Kubernetes GPU rollout and rollback | [GPU platform demo](examples/gpu-platform-sre/README.md) |
| Run benchmarks and interpret results | [Evaluation](docs/evaluation.md) · [Coverage gaps](docs/evaluation-gap-analysis.md) |
| Find a module or verification suite | [Source entry points](docs/operator-guide.md#source-code-entry-points) · [Test guide](tests/README.md) |
| Report a vulnerability or review public data | [Security policy](SECURITY.md) · [Public dataset](dataset/README.md) |

## Development and releases

See the [contribution guide](CONTRIBUTING.md) for the repository layout, build and test commands, and pull request checklist. The [changelog](CHANGELOG.md) records release and unreleased changes.

## License

Distributed under [GNU GPL v3](LICENSE).
