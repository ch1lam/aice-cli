# AICE

[English](./README.md) | 简体中文

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE)

一个小而完整的 coding agent：单一 Go 二进制、显式 Agent Loop 与工具集，
以及 append-only 的 Session 历史。

## 安装

macOS 或 Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell：

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

无需 Go 工具链。支持平台、手动下载、运行时辅助程序、源码构建和
`aice update` 参见[安装与升级](./docs/installation.md)。

## 快速开始

在项目中启动交互式 Session：

```sh
cd /path/to/project
aice --workspace .
```

首次启动后执行 `/login`，选择 `deepseek`、`opencode-go` 或 `openai`，再通过
隐藏输入填写 API Key。输入 `/help` 查看命令，输入 `?` 查看快捷键。AICE
工作期间，按 Enter 可调整当前响应，按 Ctrl+Enter 可排队一个独立的后续响应。
使用 `/btw [问题]` 可新建一个无工具、不会打断或写入主 Session 的侧线程；
输入不带参数的 `/btw` 会打开线程选择菜单；没有侧线程时则直接打开空白输入框。

执行一次非交互请求：

```sh
aice --workspace . --print "解释这个仓库的架构。"
```

`--print` 默认不保存 Session；传入 `--session` 才会创建或续接指定文件。
交互模式会自动在 `<workspace>/.aice/sessions/` 创建 Session，续接方式如下：

```sh
aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

## 当前能力

| 领域 | 当前实现 |
| --- | --- |
| 交互 | Bubble Tea TUI 与一次性 `--print` 模式 |
| Provider | DeepSeek V4、OpenCode Go 的 24 个模型与 OpenAI GPT-5.6 |
| 协议 | Anthropic Messages、OpenAI Responses、OpenAI Chat Completions |
| 工具 | `read`、`write`、`edit`、`bash`、`grep`、`find`、`ls` |
| Session | 重启恢复、分支、回退与自动/手动非破坏性压缩 |
| 侧问题 | Session 历史之外、无工具的多个临时 `/btw` 线程 |
| 输入 | 仅文本 |

内建工具继承 AICE 进程权限；`--workspace` 只是工作目录，不是沙箱。
Project Trust 只控制根目录 `AGENTS.md`、`.aice/SYSTEM.md` 和
`.aice/APPEND_SYSTEM.md` 的加载，不可信项目仍拥有完整宿主权限。

## 文档

详细文档：

- [安装与升级](./docs/installation.md)
- [配置与命令](./docs/configuration.md)
- [Project Trust 与 Prompt](./docs/project-trust.md)
- [工具执行与 Session](./docs/execution-sessions.md)
- [架构](./docs/architecture.md)与[运行时契约](./docs/contracts.md)

## 开发

使用 [`go.mod`](./go.mod) 声明的 Go 版本，并遵守
[`AGENTS.md`](./AGENTS.md) 中的仓库规则。

```sh
go test ./...
go vet ./...
```

## 当前状态

AICE 仍在快速迭代，稳定版发布前 Session 与配置格式仍可能变化。内核保持
provider-neutral；当前内建 provider 为 DeepSeek、OpenCode Go 与 OpenAI。

## 许可证

项目使用 Apache License 2.0，详见 [`LICENSE`](./LICENSE)。
