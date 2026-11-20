# AI SRE Agent

[![版本](https://img.shields.io/badge/version-v0.95-2ea44f?style=flat-square)](https://github.com/jfang2048/ai_sre_agent_pub/releases/tag/v0.95)
[![许可证](https://img.shields.io/badge/license-GPL--3.0-blue?style=flat-square)](LICENSE)
[![CI](https://github.com/jfang2048/ai_sre_agent_pub/actions/workflows/ci.yml/badge.svg?branch=v0.95)](https://github.com/jfang2048/ai_sre_agent_pub/actions/workflows/ci.yml)

[English](README.md)

AI SRE Agent 采集 Linux、Kubernetes 和 GPU 基础设施上的现场证据。值班工程师可以用它调查事故、回查结论依据，并为人工审核准备处置方案。

## 主要功能

- **调查事故。** 结合遥测和日志提出根因假设，记录处置建议与验证结果。[项目手册](docs/operator-guide.zh-CN.md#调查事故)
- **监测推理服务。** 在同一页面查看 vLLM 排队、首字延迟、输出间隔、吞吐和同节点 GPU 观测。[推理监测](docs/operator-guide.zh-CN.md#大模型推理与-gpu-监测)
- **审计部署证据。** 导入 SARIF 扫描报告和 Kubernetes 审计日志。在 **Security Dashboard → DevSecOps Audit** 查看关联事件、拒绝率异常与证据引用。[DevSecOps 审计](docs/operator-guide.zh-CN.md#devsecops-审计工作区)

## 架构

探针负责采样。采集器缓冲并主动上报。控制器应用策略，记录调查过程。界面和命令行工具通过 HTTP 接口和文件读取结果。各环节用带版本的记录和证据 ID 交接。[运行时架构](docs/operator-guide.zh-CN.md#unix-模块如何组合)

## 快速开始

在 Linux 上安装 Git、GNU Make、Bash、curl、Go 1.26.11，以及带 npm 的 Node.js 22.12 或更高的 22.x 版本。[原生探针依赖](docs/operator-guide.zh-CN.md#1-准备构建环境)

```bash
git clone --branch v0.95 https://github.com/jfang2048/ai_sre_agent_pub.git
cd ai_sre_agent_pub
npm -C frontend ci
make run-both
```

打开 <http://127.0.0.1:8080/>，实际地址以启动输出为准。在另一个终端运行以下命令确认控制器就绪，预期返回 `OK`。

```bash
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
```

在控制台确认采集器在线并查看最新采样。在启动终端按 `Ctrl+C` 停止服务。[本地配置与演示](docs/operator-guide.zh-CN.md#启动本地环境)

## 文档导航

| 我想做什么 | 从这里开始 |
| --- | --- |
| 部署控制器和采集器 | [部署说明](deploy/README.md) · [遥测持久化](deploy/telemetry-durability.md) |
| 查看生产变更的执行规则 | [工具契约与执行策略](docs/operator-guide.zh-CN.md#工具契约与执行策略) |
| 演练 Kubernetes GPU 发布与回滚 | [GPU 平台演示](examples/gpu-platform-sre/README.md) |
| 运行评估并解读结果 | [评估指南](docs/evaluation.zh-CN.md) · [有效性验证](docs/effectiveness-validation.zh-CN.md) |
| 查源码或选择测试套件 | [源码入口](docs/operator-guide.zh-CN.md#源码入口) · [测试指南](tests/README.md) |
| 报告漏洞或查看公开数据范围 | [安全说明](SECURITY.md) · [公开数据集](dataset/README.md) |

## 开发与发布

[贡献指南](CONTRIBUTING.md)列出仓库结构、构建与测试命令以及 PR 检查步骤。[更新日志](CHANGELOG.md)记录已发布和待发布的变更。

## 许可证

项目采用 [GNU GPL v3](LICENSE)。
