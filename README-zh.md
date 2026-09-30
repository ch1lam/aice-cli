# AICE

<p align="center">
  <img src="./assets/aice-header.png" alt="AICE coding agent" width="900">
</p>

[English](./README.md) | 简体中文

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE) [![Build](https://img.shields.io/github/actions/workflow/status/ch1lam/aice-cli/ci.yml?branch=main&label=build)](https://github.com/ch1lam/aice-cli/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/ch1lam/aice-cli)](https://github.com/ch1lam/aice-cli/releases/latest)

一个小巧的 coding agent：单一 Go 二进制、显式 Agent Loop 与工具集，
以及只追加的 Session 历史。

## 安装

macOS 或 Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell：

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

无需 Go 工具链。支持平台、辅助程序、手动下载和源码构建见[安装与升级](./docs/installation.md)。

## 快速开始

```sh
cd /path/to/project
aice --workspace .
```

执行 `/login`，选择账号或 API Key 登录，再选择 provider。
用 `/model` 选择模型、`/settings` 修改偏好、`/help` 查看命令、`?` 查看快捷键。
各 provider 的接入方法和配置优先级见[配置](./docs/configuration.md)。

用 `@src/main.go` 引用文件；使用视觉模型时，可用 `Ctrl+V` 或 `Alt+V` 粘贴图片。
AICE 工作期间，Enter 调整当前响应、Ctrl+Enter 排队后续交互、Esc 中断响应。
`/btw` 打开无工具的侧对话。

执行一次非交互请求：

```sh
aice --workspace . --print "解释这个仓库的架构。"
```

`--output-format json` 输出 NDJSON 事件。Print 模式默认不保存 Session，
传入 `--session` 才会保存。交互历史位于 `<workspace>/.aice/sessions/`，
用 `/history` 或 Ctrl+R 查找并恢复。详见[执行与 Session](./docs/execution-sessions.md)。

## 当前能力

| 领域 | 入口与范围 |
| --- | --- |
| 模型 | DeepSeek、OpenCode Go、Kimi Coding Plan、Moonshot API、智谱 API/Coding Plan、OpenAI、Claude API/Pro/Max、Codex/ChatGPT、AiHubMix，以及自定义 OpenAI 兼容端点；见[配置](./docs/configuration.md) |
| 编程 | 文件读取与编辑、Bash、ripgrep、图片输入和 `SKILL.md` 指令 |
| 联网 | `/web`：配置 Exa 搜索与公开网页抓取；见[指南](./docs/web.md) |
| 浏览器 | `/browser`：通过原生 agent-browser 辅助程序控制窗口显示、外部连接与标签页；见[指南](./docs/browser.md) |
| MCP | `aice mcp`、`/mcp` 或 Settings：stdio/HTTP 服务、OAuth、按需工具发现与资源；见[指南](./docs/mcp.md) |
| Computer Use | `/desktop`：显式设置与状态，默认关闭；见[平台支持与限制](./docs/desktop.md) |
| Session | 搜索、恢复、分支、回退和自动/手动压缩；见[指南](./docs/execution-sessions.md) |

工具继承 AICE 进程权限，每次调用经过 Guard。`--workspace` 定义工作目录与路径访问边界，
不是沙箱。Print 模式遇到授权请求会拒绝，除非使用 `--yolo`；明确的 deny 仍生效。
启用的 Web 工具自动访问网络，无需额外确认；Guard 不隔离浏览器页面内部操作。
详见[执行权限](./docs/execution-sessions.md#tool-execution-boundary)与
[Project Trust](./docs/project-trust.md)。需要主机隔离时使用容器或 VM。

## 开发

使用 [go.mod](./go.mod) 中声明的 Go 版本，并遵守 [AGENTS.md](./AGENTS.md)。
先读[架构](./docs/architecture.md)，代码入口和已知偏差见[维护指南](./docs/maintenance.md)，
必需检查见[协作规范](./docs/collaboration.md)。

```sh
go test ./...
go vet ./...
```

AICE 仍在开发中，稳定版发布前 Session 与配置格式可能变化。

## 许可证

Apache License 2.0，详见 [LICENSE](./LICENSE)。
