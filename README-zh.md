# AICE

[English](./README.md) | 简体中文

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE)

一个小而完整的 coding agent：单一 Go 二进制、一个 Agent Loop、显式工具，
以及 append-only 的 Session 真相。

## 安装

一条命令安装最新版（macOS / Linux）：

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows（PowerShell）：

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

无需安装 Go 工具链。安装器会把 `aice` 放到用户可写目录（`~/.local/bin`，
Windows 为 `%USERPROFILE%\.local\bin`），后续 `aice update` 可以直接原地替换
二进制。首次启动时 AICE 会自动检测并下载缺失的辅助程序。

### 环境要求

- `bash` 和 [`ripgrep`](https://github.com/BurntSushi/ripgrep)（`rg`）——
  缺失时 app 会在首次启动时自动下载
- 一个 provider 的 API Key（DeepSeek，或 OpenCode Go 的 `OPENCODE_API_KEY`）

### 手动下载

每个 [GitHub Release](https://github.com/ch1lam/aice-cli/releases) 都会附带
macOS（`arm64`、`amd64`）、Linux（`arm64`、`amd64`）和 Windows（`amd64`）
的预编译二进制。Apple Silicon（M 系列）Mac：

```sh
curl -fL -o aice.tar.gz \
  https://github.com/ch1lam/aice-cli/releases/latest/download/aice_darwin_arm64.tar.gz
tar -xzf aice.tar.gz
sudo mv aice /usr/local/bin/
aice --version
```

其他机器选择对应压缩包：`aice_darwin_amd64.tar.gz`（Intel Mac）、
`aice_linux_amd64.tar.gz`、`aice_linux_arm64.tar.gz`、`aice_windows_amd64.zip`。
`latest/download/` 链接始终指向最新版本；旧版本仍可从对应 tag 的 Release 页面下载。

### 升级

```sh
aice update            # 安装最新版
aice update --check    # 只报告是否存在新版
aice update --force    # 强制重装, 或覆盖 dev 构建
```

每次下载的资产都会先对照该 Release 的 `checksums.txt` 校验，再替换可执行文件。
在交互式终端中，AICE 每天最多静默检查一次新版本，若有新版会提示
`update available: X -> Y (run \`aice update\`)`；设置 `AICE_NO_UPDATE_CHECK=1`
可关闭该检查。由包管理器安装的 AICE 无法自更新——`aice update` 会报告所使用的
包管理器，并提示用其升级（例如 `brew upgrade aice`）。

设置 `AICE_NO_DEP_INSTALL=1` 可禁用辅助程序的自动下载，改为手动管理。

## 快速开始

在项目目录中启动 AICE：

```sh
cd /path/to/project
aice --workspace .
```

首次启动后执行 `/login`，选择 provider，TUI 会隐藏输入 API Key，并保存到
`~/.aice/auth.json`。输入 `/help` 查看命令，输入 `?` 查看快捷键。

执行一次非交互请求：

```sh
aice --workspace . --print "解释这个仓库的架构。"
```

### Session

交互 Session 会自动创建在 `<workspace>/.aice/sessions/`。使用 `/session`
查看当前文件，需要时通过文件路径继续：

```sh
aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

### 项目信任

AICE 只有在项目信任决策通过后才加载项目本地 Prompt
（`<workspace>/.aice/SYSTEM.md` 与 `APPEND_SYSTEM.md`）。首次进入带这类文件的
项目时会询问是否信任；`--approve` / `--no-approve` 为自动化运行做出选择，
`/trust` 管理已保存的决定。不信任的项目仍以内建 system prompt 和完整宿主权限
运行；Project Trust 不是沙箱。

### 源码构建

环境要求：[`go.mod`](./go.mod) 中声明的 Go 版本。

```sh
git clone https://github.com/ch1lam/aice-cli.git
cd aice-cli
go build -o ./aice ./cmd/aice
./aice --workspace /path/to/project
```

## 功能特性

- **单一小型运行时** — 单一 Go module、二进制和进程，不引入 daemon、Node
  bridge、全局注册表或依赖注入框架。
- **Session 是事实源** — 完整对话、压缩检查点和 active leaf 移动以 JSONL
  追加写入；上下文由当前分支派生，回退与压缩不会改写旧记录。
- **边界可替换** — AICE 自己拥有消息、事件、用量、工具与循环语义，provider
  SDK 类型不会泄漏进内核。
- **宿主执行模型** — 内建工具继承 AICE 进程权限；`--workspace`
  只指定默认工作目录，不是沙箱或访问边界。

### 当前能力

| 领域 | 当前实现 |
| --- | --- |
| 交互 | Bubble Tea TUI 与一次性 `--print` 模式 |
| Provider | DeepSeek（V4 Flash 走 OpenAI Responses，V4 Pro 走 Anthropic Messages）与 OpenCode Go（24 个模型走 OpenAI Chat Completions） |
| 工具 | `read`、`write`、`edit`、`bash`、`grep`、`find`、`ls` |
| Session | 重启恢复、分支树、回退和手动非破坏性压缩 |
| 输入 | 仅文本 |

## 文档

- [配置](./docs/configuration.md) — 设置、环境变量、凭据、交互式斜杠命令
- [架构](./docs/architecture.md) — 项目方向与预期结构
- [契约](./docs/contracts.md) — LLM 与 Agent 的消息/工具契约
- [执行与 Session](./docs/execution-sessions.md) — 工具执行、沙箱边界、压缩
- [Go 质量](./docs/go-quality.md) — 编码标准

模型与 reasoning 配置保存在 `~/.aice/settings.json`，凭据单独存储。

## 当前状态

AICE 仍在快速迭代。当前内建 provider 是 DeepSeek 与 OpenCode Go、输入仅支持
文本，稳定版本发布前 Session 与配置格式仍可能变化。所谓 provider-neutral
指内核边界，不代表多 provider 已经完成。

## 开发

保持改动小而明确，并遵守 [`AGENTS.md`](./AGENTS.md) 中的架构不变量。

```sh
go test ./...
go vet ./...
```

## 许可证

项目使用 Apache License 2.0，详见 [`LICENSE`](./LICENSE)。
