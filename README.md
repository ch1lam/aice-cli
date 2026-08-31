# AICE

<p align="center">
  <img src="./assets/aice-header.png" alt="AICE coding agent" width="900">
</p>

English | [简体中文](./README-zh.md)

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE) [![Build](https://img.shields.io/github/actions/workflow/status/ch1lam/aice-cli/ci.yml?branch=main&label=build)](https://github.com/ch1lam/aice-cli/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/ch1lam/aice-cli)](https://github.com/ch1lam/aice-cli/releases/latest)

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

On first launch, run `/login`, select `deepseek`, `opencode-go`, `openai`, or
`custom`, and complete that provider's credential flow. Run `/help` for
commands or `?` for keyboard shortcuts. While AICE is working, Enter steers
the active response and Ctrl+Enter queues a follow-up interaction in the same
Agent run. Use `/btw [question]` to start a new tool-free side thread that
does not interrupt or enter the main Session. Bare `/btw` opens the thread
chooser, or a blank composer when no side threads exist.

Run one non-interactive request:

```sh
aice --workspace . --print "Explain the architecture of this repository."
```

Use `--output-format json` for a machine-readable NDJSON event stream; default
text mode keeps answer text on stdout and reports progress on stderr. See
[Configuration and commands](./docs/configuration.md#command-line-options).

`--print` is stateless unless `--session` is supplied. Interactive runs create
a Session automatically under `<workspace>/.aice/sessions/`; resume one with:

```sh
aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

## Included

| Area | Current implementation |
| --- | --- |
| Interface | Bubble Tea TUI and one-shot `--print` mode |
| Providers | DeepSeek V4, OpenCode Go's 22-model catalog, OpenAI GPT-5.6, and Custom (OpenAI-compatible) |
| Protocols | Anthropic Messages, OpenAI Responses, OpenAI Chat Completions |
| Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls`, `skill` |
| Guard | intrinsic execution gate (`internal/guard`): pathAccess mode `allow`/`ask`/`block`; Decision `allow`/`ask`/`deny`; see [Tool execution and Sessions](./docs/execution-sessions.md#tool-execution-boundary) |
| Sessions | restart recovery, branches, checkout/backtracking, automatic and manual compaction |
| Side questions | multiple ephemeral, tool-free `/btw` threads outside Session history |
| Agent Skills | open-spec `SKILL.md` directories from builtin, `~/.agents/skills`, and project `.agents/skills`; see [Agent Skills](./docs/configuration.md#agent-skills) |
| Input | text only |

Tools inherit the permissions of the AICE process. Every call is checked by
the execution gate; `--print` treats `ask` as `deny` unless `--yolo`. `--workspace` sets the
working directory and path-access boundary; it is not a sandbox. Project Trust
gates project prompt files and project `.agents/skills`; see [Project Trust and
prompts](./docs/project-trust.md). For stronger isolation use an external
container/VM.

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
built-in provider set is currently DeepSeek, OpenCode Go, OpenAI, and Custom.

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).
