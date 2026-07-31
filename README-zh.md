# AICE

[English](./README.md) | [简体中文](./README-zh.md)

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE)

> 一个小而完整的 coding agent：单一 Go 二进制、一个 Agent Loop、显式工具，
> 以及 append-only 的 Session 真相。

AICE 是一个受 [Pi](https://github.com/earendil-works/pi) 启发、使用纯 Go
实现的编码代理。它不是通用 Agent 框架，也不是 TypeScript 代码的翻译版。
项目刻意保持一个小内核：provider-neutral 消息、自然终止的 Agent Loop、
编码工具和持久化 Session；CLI、TUI、provider、协议与存储都停留在明确边界。

## 项目精髓

- **单一小型运行时** — 单一 Go module、二进制和进程，不引入 daemon、Node
  bridge、全局注册表或依赖注入框架。
- **Session 是事实源** — 完整对话、压缩检查点和 active leaf 移动以 JSONL
  追加写入；上下文由当前分支派生，回退与压缩不会改写旧记录。
- **边界可替换** — AICE 自己拥有消息、事件、用量、工具与循环语义，provider
  SDK 类型不会泄漏进内核。
- **宿主执行模型** — 内建工具继承 AICE 进程权限；`--workspace`
  只指定默认工作目录，不是沙箱或访问边界。

## 当前能力

| 领域 | 当前实现 |
| --- | --- |
| 交互 | Bubble Tea TUI 与一次性 `--print` 模式 |
| Provider | 通过 Anthropic Messages 协议调用 DeepSeek V4 Flash 与 Pro |
| 工具 | `read`、`write`、`edit`、`bash`、`grep`、`find`、`ls` |
| Session | 重启恢复、分支树、回退和手动非破坏性压缩 |
| 输入 | 仅文本 |

## 快速开始

环境要求：

- [`go.mod`](./go.mod) 中声明的 Go 版本
- `PATH` 中存在 `bash` 和
  [`ripgrep`](https://github.com/BurntSushi/ripgrep)（`rg`）
- DeepSeek API Key

```sh
git clone https://github.com/ch1lam/aice-cli.git
cd aice-cli
go build -o ./aice ./cmd/aice
./aice --workspace /path/to/project
```

首次启动后执行 `/login`。TUI 会隐藏输入 API Key，并保存到
`~/.aice/auth.json`。输入 `/help` 查看命令，输入 `?` 查看快捷键。

执行一次非交互请求：

```sh
./aice --workspace . --print "解释这个仓库的架构。"
```

交互 Session 会自动创建在 `<workspace>/.aice/sessions/`。使用 `/session`
查看当前文件，需要时通过文件路径继续：

```sh
./aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

模型与 reasoning 配置保存在 `~/.aice/settings.json`，凭据单独存储。环境变量与
交互命令详见[配置文档](./docs/configuration.md)。

## 当前状态

AICE 仍在快速迭代。当前内建 provider 只有 DeepSeek、输入仅支持文本，稳定版本
发布前 Session 与配置格式仍可能变化。所谓 provider-neutral 指内核边界，
不代表多 provider 已经完成。

## 开发

保持改动小而明确，并遵守 [`AGENTS.md`](./AGENTS.md) 中的架构不变量。

```sh
go test ./...
go vet ./...
```

## 许可证

项目使用 Apache License 2.0，详见 [`LICENSE`](./LICENSE)。
