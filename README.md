# AICE

English | [简体中文](./README-zh.md)

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE)

A small, batteries-included coding agent: one Go binary, one Agent Loop,
explicit tools, and append-only Session truth.

## Install

Install the latest release with a single command (macOS and Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

No Go toolchain required. The installer puts `aice` in a user-writable
directory (`~/.local/bin`, or `%USERPROFILE%\.local\bin` on Windows) so
`aice update` can replace the binary in place later. On first launch AICE
detects its helper executables and downloads the missing ones automatically.

### Requirements

- `bash` and [`ripgrep`](https://github.com/BurntSushi/ripgrep) (`rg`) — the
  app downloads them automatically on first launch if they are missing
- a provider API key (DeepSeek, or OpenCode Go via `OPENCODE_API_KEY`)

### Manual download

Prebuilt binaries are attached to each [GitHub
Release](https://github.com/ch1lam/aice-cli/releases) for macOS (`arm64`,
`amd64`), Linux (`arm64`, `amd64`), and Windows (`amd64`). On Apple Silicon
(M-series) macOS:

```sh
curl -fL -o aice.tar.gz \
  https://github.com/ch1lam/aice-cli/releases/latest/download/aice_darwin_arm64.tar.gz
tar -xzf aice.tar.gz
sudo mv aice /usr/local/bin/
aice --version
```

Pick the matching archive for other machines: `aice_darwin_amd64.tar.gz`
(Intel Mac), `aice_linux_amd64.tar.gz`, `aice_linux_arm64.tar.gz`, or
`aice_windows_amd64.zip`. The `latest/download/` URL always resolves to the
newest release; older versions remain available from their tagged release
page.

### Update

```sh
aice update            # install the newest release
aice update --check    # only report whether an update exists
aice update --force    # reinstall, or install over a dev build
```

Each downloaded asset is verified against the release's `checksums.txt`
before it replaces the executable. In interactive terminals AICE silently
checks for a new release at most once a day and hints `update available:
X -> Y (run \`aice update\`)`; set `AICE_NO_UPDATE_CHECK=1` to disable that
check. Installs owned by a package manager cannot self-update — `aice
update` reports the manager and asks you to upgrade with it (for example
`brew upgrade aice`).

Set `AICE_NO_DEP_INSTALL=1` to disable the automatic helper downloads and
manage helpers yourself.

## Quickstart

Run AICE inside your project:

```sh
cd /path/to/project
aice --workspace .
```

On first launch, run `/login`, pick a provider, and enter its API key through
hidden input. Keys are stored in `~/.aice/auth.json`. Type `/help` for
commands or `?` for shortcuts.

Run one non-interactive request:

```sh
aice --workspace . --print "Explain the architecture of this repository."
```

### Sessions

Interactive sessions are created automatically under
`<workspace>/.aice/sessions/`. Use `/session` to see the current file, then
resume it explicitly when needed:

```sh
aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

### Project trust

AICE loads project-local prompts (`<workspace>/.aice/SYSTEM.md` and
`APPEND_SYSTEM.md`) only after a project trust decision. The first time you
enter a project with such files, AICE asks whether to trust it; `--approve`
and `--no-approve` make that choice for automated runs, and `/trust` manages
saved decisions. Untrusted projects run with the built-in system prompt and
full host permissions; Project Trust is not a sandbox.

### Build from source

Requirements: the Go version declared in [`go.mod`](./go.mod).

```sh
git clone https://github.com/ch1lam/aice-cli.git
cd aice-cli
go build -o ./aice ./cmd/aice
./aice --workspace /path/to/project
```

## Features

- **One small runtime** — one Go module, binary, and process; no daemon, Node
  bridge, global registry, or dependency-injection framework.
- **Session is the source of truth** — complete turns, compaction checkpoints,
  and active-leaf moves are appended to a local JSONL tree. Context is derived
  from the selected branch; checkout and compaction never rewrite old turns.
- **Replaceable edges** — AICE owns messages, events, usage, tools, and loop
  semantics. Provider SDK types stay behind protocol adapters.
- **Host-owned execution** — built-in tools run with the permissions of the
  AICE process. `--workspace` selects their default working directory; it is
  not a sandbox or access boundary.

### Current scope

| Area | Current implementation |
| --- | --- |
| Interface | Bubble Tea TUI and one-shot `--print` mode |
| Provider | DeepSeek (V4 Flash via OpenAI Responses, V4 Pro via Anthropic Messages) and OpenCode Go (24 models via OpenAI Chat Completions) |
| Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls` |
| Sessions | restart recovery, branch tree, checkout/backtracking, and manual non-destructive compaction |
| Input | text only |

## Documentation

- [Configuration](./docs/configuration.md) — settings, environment
  variables, credentials, interactive slash commands
- [Architecture](./docs/architecture.md) — project direction and intended
  structure
- [Contracts](./docs/contracts.md) — LLM and Agent message/tool contracts
- [Execution & sessions](./docs/execution-sessions.md) — tool execution,
  sandbox boundaries, compaction
- [Go quality](./docs/go-quality.md) — coding standards

Model and reasoning settings live in `~/.aice/settings.json`; credentials
are stored separately.

## Status

AICE is under active development. Built-in providers are DeepSeek and OpenCode
Go, input is text-only, and Session/config formats may evolve before a stable
release. The provider-neutral core describes an architectural boundary, not a
claim of finished multi-provider support.

## Development

Keep changes small and preserve the invariants in
[`AGENTS.md`](./AGENTS.md).

```sh
go test ./...
go vet ./...
```

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).
