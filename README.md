# AICE

English | [简体中文](./README-zh.md)

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE)

A small, batteries-included coding agent: one Go binary, an explicit Agent
Loop and tool set, and append-only Session history.

## Install

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

No Go toolchain is required. See [Installation and
updates](./docs/installation.md) for supported platforms, manual downloads,
runtime helpers, source builds, and `aice update`.

## Quickstart

Start an interactive Session inside a project:

```sh
cd /path/to/project
aice --workspace .
```

On first launch, run `/login`, select `deepseek`, `opencode-go`, or `openai`,
and enter the API key through hidden input. Run `/help` for commands or `?` for
keyboard shortcuts. While AICE is working, Enter steers the active response
and Ctrl+Enter queues a follow-up interaction in the same Agent run. Use
`/btw [question]` for a tool-free side question that does not interrupt or
enter the main Session; use bare `/btw` to reopen its ephemeral panel.

Run one non-interactive request:

```sh
aice --workspace . --print "Explain the architecture of this repository."
```

`--print` is stateless unless `--session` is supplied. Interactive runs create
a Session automatically under `<workspace>/.aice/sessions/`; resume one with:

```sh
aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

## Included

| Area | Current implementation |
| --- | --- |
| Interface | Bubble Tea TUI and one-shot `--print` mode |
| Providers | DeepSeek V4, OpenCode Go's 24-model catalog, and OpenAI GPT-5.6 |
| Protocols | Anthropic Messages, OpenAI Responses, OpenAI Chat Completions |
| Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls` |
| Sessions | restart recovery, branches, checkout/backtracking, manual compaction |
| Side questions | ephemeral, tool-free `/btw` thread outside Session history |
| Input | text only |

The default tools run with the permissions of the AICE process.
`--workspace` sets their working directory; it is not a sandbox. Project Trust
only gates root `AGENTS.md`, `.aice/SYSTEM.md`, and
`.aice/APPEND_SYSTEM.md`; untrusted projects still have full host permissions.

## Documentation

Detailed guides:

- [Installation and updates](./docs/installation.md)
- [Configuration and commands](./docs/configuration.md)
- [Project Trust and prompts](./docs/project-trust.md)
- [Tool execution and Sessions](./docs/execution-sessions.md)
- [Architecture](./docs/architecture.md) and [runtime contracts](./docs/contracts.md)

## Development

Use the Go version in [`go.mod`](./go.mod), and preserve the repository rules
in [`AGENTS.md`](./AGENTS.md).

```sh
go test ./...
go vet ./...
```

## Status

AICE is under active development. Session and configuration formats may still
change before a stable release. The core is provider-neutral, while the
built-in provider set is currently DeepSeek, OpenCode Go, and OpenAI.

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).
