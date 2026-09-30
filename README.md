# AICE

<p align="center">
  <img src="./assets/aice-header.png" alt="AICE coding agent" width="900">
</p>

English | [简体中文](./README-zh.md)

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE) [![Build](https://img.shields.io/github/actions/workflow/status/ch1lam/aice-cli/ci.yml?branch=main&label=build)](https://github.com/ch1lam/aice-cli/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/ch1lam/aice-cli)](https://github.com/ch1lam/aice-cli/releases/latest)

A small coding agent: one Go binary, an explicit Agent Loop and tool set,
and append-only Session history.

## Install

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

No Go toolchain is required. See [Installation and updates](./docs/installation.md)
for supported platforms, helpers, manual downloads, and source builds.

## Quickstart

```sh
cd /path/to/project
aice --workspace .
```

Run `/login`, choose account or API-key authentication, and select a provider.
Use `/model` to select a model, `/settings` to edit preferences, `/help` for
commands, and `?` for keyboard shortcuts. Provider setup and configuration
precedence are in [Configuration](./docs/configuration.md).

Attach a file with `@src/main.go`; paste an image with `Ctrl+V` or `Alt+V` when
using a vision model. While AICE works, Enter steers the response, Ctrl+Enter
queues a follow-up, and Esc cancels. `/btw` opens a tool-free side conversation.

Run one non-interactive request:

```sh
aice --workspace . --print "Explain the architecture of this repository."
```

`--output-format json` emits NDJSON events. Print mode is stateless unless
`--session` is supplied. Interactive history is stored under
`<workspace>/.aice/sessions/`; use `/history` or Ctrl+R to find and resume it.
See [Execution and Sessions](./docs/execution-sessions.md).

## Included

| Area | Entry point and scope |
| --- | --- |
| Models | DeepSeek, OpenCode Go, Kimi Coding Plan, Moonshot API, Zhipu API/Coding Plan, OpenAI, Claude API/Pro/Max, Codex/ChatGPT, AiHubMix, and custom OpenAI-compatible endpoints; [setup](./docs/configuration.md) |
| Coding | File reading/editing, Bash, ripgrep, image input, and `SKILL.md` instructions |
| Web | `/web`: configure Exa search and public-page fetching; [guide](./docs/web.md) |
| Browser | `/browser`: window visibility, external connections, and tab selection through the native agent-browser helper; [guide](./docs/browser.md) |
| MCP | `aice mcp`, `/mcp`, or Settings: stdio/HTTP services, OAuth, on-demand tool discovery, and resources; [guide](./docs/mcp.md) |
| Computer Use | `/desktop`: explicit setup and status, disabled by default; [platform support and limits](./docs/desktop.md) |
| Sessions | Search, resume, branches, backtracking, and automatic/manual compaction; [guide](./docs/execution-sessions.md) |

Tools inherit AICE's process permissions and pass through Guard. `--workspace`
is a working directory and path-access boundary, not a sandbox. Print mode fails
closed on approval requests unless `--yolo`; explicit denials still apply.
Enabled web tools access the network without extra confirmation. Browser actions
inside a page are not isolated by Guard. See [execution permissions](./docs/execution-sessions.md#tool-execution-boundary)
and [Project Trust](./docs/project-trust.md). Use a container/VM when host isolation
is required.

## Development

Use the Go version in [go.mod](./go.mod) and follow [AGENTS.md](./AGENTS.md).
Start with [Architecture](./docs/architecture.md); [Maintenance](./docs/maintenance.md)
indexes code and known discrepancies. Required checks are in
[Collaboration](./docs/collaboration.md).

```sh
go test ./...
go vet ./...
```

AICE is under active development; Session and configuration formats may change
before a stable release.

## License

Apache License 2.0. See [LICENSE](./LICENSE).
