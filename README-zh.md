# AICE

**面向长期软件维护的开源 Coding Agent Harness。**

AICE 希望帮助开发者持续写出易读、可测试、易修改的代码，服务于日常开发、
代码库重构和文档迁移，以代码质量为核心目标。

目前，AICE 提供模型调用、工具执行、上下文与会话管理等运行时能力，
通过终端界面（TUI）使用，模型服务可自由选择。

<p align="center">
  <img src="./assets/aice-header.png" alt="AICE coding agent" width="900">
</p>

[English](./README.md) | 简体中文

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE) [![Build](https://img.shields.io/github/actions/workflow/status/ch1lam/aice-cli/ci.yml?branch=main&label=build)](https://github.com/ch1lam/aice-cli/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/ch1lam/aice-cli)](https://github.com/ch1lam/aice-cli/releases/latest)

[快速开始](#快速开始) · [文档](./docs/README.md) · [Roadmap](./ROADMAP.md) · [参与贡献](./CONTRIBUTING.md)

## 适合谁？

需要长期维护和持续演进代码库的开发者与项目维护者。

| 工作 | 示例请求 |
| --- | --- |
| 日常开发 | “遵循项目约定实现这个功能，补充测试和文档。” |
| 渐进式重构 | “在保持行为不变的前提下简化这个模块，并用测试验证。” |
| 大型代码库重构 | “梳理依赖关系，制定迁移步骤，分阶段修改并验证。” |
| 文档维护与迁移 | “对照代码检查文档，删除过时内容，重新组织使用指南。” |

## 当前能力

- **选择适合的模型。** 支持 OpenAI、Claude、DeepSeek、Kimi 等模型服务，以及自定义 OpenAI 兼容端点。详见[支持的服务与登录方式](./docs/configuration.md#credentials-and-connection-overrides)。
- **操作代码库。** 读取、搜索和编辑文件，执行 Shell 命令、构建和测试。
- **随时参与。** 引用文件、粘贴截图，执行中补充要求、排队后续任务或中断运行。
- **随时继续工作。** 搜索和恢复历史会话，从之前的消息创建分支；自动上下文压缩支持较长的任务。
- **扩展工作流。** 通过 [MCP](./docs/mcp.md) 接入外部工具，通过 [Agent Skills](./docs/configuration.md#agent-skills) 复用任务指令。

## 安装

AICE 使用 Go 编写，以单个二进制分发，支持 macOS、Linux 和 Windows，
安装发行版无需 Go 工具链。

macOS 或 Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell：

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

AICE 会按需准备辅助工具，浏览器和桌面自动化另有平台要求。
详细说明、手动下载和源码构建见[安装文档](./docs/installation.md)。使用 `aice update` 升级。

## 快速开始

在项目目录打开终端：

```sh
cd /path/to/project
aice
```

执行 `/login`，通过 API Key 或受支持的订阅账号配置模型服务，再用 `/model` 选择模型。
然后直接输入任务，例如：

```text
介绍这个项目的代码结构，告诉我应该从哪里开始阅读。
```

- 输入 `@src/main.go` 引用文件；使用视觉模型时，通过 `Ctrl+V` 或 `Alt+V` 粘贴图片。
- 执行中按 Enter 补充要求，Ctrl+Enter 排队后续任务，Esc 中断运行。
- 用 `/history` 恢复历史会话、`/settings` 修改设置，`/help` 或 `?` 查看命令和快捷键。

配置好模型服务后，可通过 `--print` 执行非交互任务或接入脚本：

```sh
aice --print "总结这个仓库中的代码变更。"
```

添加 `--output-format json` 输出 NDJSON 事件，或用 `--session <path>` 保存或恢复会话。
Print 模式不弹出交互式授权提示，默认拒绝需要确认的工具调用。详见 [CLI 参数](./docs/configuration.md#command-line-options)。

## 工具与集成

| 能力 | 配置入口与文档 |
| --- | --- |
| 联网搜索与网页抓取 | `/web` — 配置 Exa 搜索和公开网页抓取；[指南](./docs/web.md) |
| 浏览器自动化 | `/browser` — 通过 agent-browser 浏览和操作网页，支持 macOS/Linux；[指南](./docs/browser.md) |
| Computer Use | `/desktop` — 显式启用基于 Cua Driver 的桌面自动化；[平台限制与验证状态](./docs/desktop.md#platform-evidence) |
| MCP 服务 | `/mcp` 或 `aice mcp` — 接入外部工具与资源；[指南](./docs/mcp.md) |
| Agent Skills | `/skills` — 查看可复用的 `SKILL.md` 任务指令；[指南](./docs/configuration.md#agent-skills) |

工具使用当前用户的本机权限执行，工作目录不是操作系统沙箱。
模型请求会将任务上下文发送给你配置的模型服务。处理敏感项目之前，请了解
[工具权限](./docs/execution-sessions.md#tool-execution-boundary)和 [Project Trust](./docs/project-trust.md)。

## Roadmap

接下来的重点是日常使用的可靠性、代码质量评估和软件维护工作流。
长期方向包括共用运行时的多种界面、云端 Agent Bot、内置调试能力和 Harness 自主改进。
当前基础、优先事项和未来计划见 [Roadmap](./ROADMAP.md)。

## 文档与社区

- [文档索引](./docs/README.md)：安装、配置、工具集成与架构。
- [Issues](https://github.com/ch1lam/aice-cli/issues/new/choose)：提问、报告问题或提出建议，中英文均可。
- [贡献指南](./CONTRIBUTING.md)：开发环境、验证要求与 PR 流程。
- [安全政策](./SECURITY.md)与[行为准则](./CODE_OF_CONDUCT.md)：报告渠道和社区约定。
- [Releases](https://github.com/ch1lam/aice-cli/releases)：下载和版本更新说明。

AICE 由 [@ch1lam](https://github.com/ch1lam) 维护，目前仍在开发中，
稳定版发布前 Session 与配置格式可能变化。

## 许可证

[Apache-2.0](./LICENSE)。
