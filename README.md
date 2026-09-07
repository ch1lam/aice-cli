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

On first launch, run `/login`, choose account or API key authentication, then
select a provider and complete its login flow. Run `/help` for
commands or `?` for keyboard shortcuts. While AICE is working, Enter steers
the active response and Ctrl+Enter queues a follow-up interaction in the same
Agent run. Use `/btw [question]` to start a new tool-free side thread that
does not interrupt or enter the main Session. Bare `/btw` opens the thread
chooser, or a blank composer when no side threads exist.

For a ChatGPT/Codex subscription, use `/login` → `Sign in with an account` →
`OpenAI Codex`, then choose browser or device code login. Browser login opens
its authorization page and accepts a callback or pasted redirect URL; Escape
or Ctrl+C cancels. The terminal alternative is `aice auth login --provider
openai-codex` (add `--device-code` for headless login).
See [Codex subscription setup](./docs/configuration.md#codex-subscription-chatgpt-oauth).

For Kimi Coding Plan, select `/login` → `Sign in with an API key` →
`Kimi Coding Plan`. See [Kimi setup](./docs/configuration.md#kimi-coding-plan).
For the separately billed China API platform, choose `Moonshot API` and enter
your platform key; its endpoint is built in. See [Moonshot setup](./docs/configuration.md#moonshot-api-platform).

For Zhipu, choose `Zhipu API` or `Zhipu Coding Plan` in the API-key login menu. See
[Zhipu setup](./docs/configuration.md#zhipu-api-platform).

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
| Interface | Bubble Tea TUI with [context usage percentage](./docs/configuration.md#context-window-and-status-bar) per provider/model, and one-shot `--print` mode |
| Providers | DeepSeek V4, OpenCode Go's built-in catalog, Kimi Coding Plan (Responses API), Moonshot API Platform, Zhipu API Platform and Coding Plan, OpenAI GPT-5.6 API, Codex/ChatGPT subscription, and Custom (OpenAI-compatible) |
| Protocols | Anthropic Messages, OpenAI Responses, OpenAI Chat Completions |
| Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls`, `skill` |
| Guard | path and dangerous-command checks with interactive approvals; see [Tool execution and Sessions](./docs/execution-sessions.md#tool-execution-boundary) |
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
- [Architecture](./docs/architecture.md), [runtime contracts](./docs/contracts.md), and [maintenance / known discrepancies](./docs/maintenance.md)

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
built-in provider set is currently DeepSeek, OpenCode Go, Kimi Coding Plan, Moonshot API, Zhipu API/Coding Plan, OpenAI, Codex, and Custom.

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).
