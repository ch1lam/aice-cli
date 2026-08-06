# AICE

English | [简体中文](./README-zh.md)

[![Go](https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli)](https://go.dev/) [![License](https://img.shields.io/github/license/ch1lam/aice-cli)](./LICENSE)

> A small, batteries-included coding agent: one Go binary, one Agent Loop,
> explicit tools, and append-only Session truth.

AICE is a pure-Go coding agent inspired by
[Pi](https://github.com/earendil-works/pi). It is not a general-purpose agent
framework or a TypeScript port. Its center stays deliberately small:
provider-neutral messages, a natural Agent Loop, coding tools, and durable
Sessions; the CLI, TUI, providers, protocols, and persistence remain explicit
edges.

## What matters

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

## Current scope

| Area | Current implementation |
| --- | --- |
| Interface | Bubble Tea TUI and one-shot `--print` mode |
| Provider | DeepSeek (V4 Flash via OpenAI Responses, V4 Pro via Anthropic Messages) and OpenCode Go (24 models via OpenAI Chat Completions) |
| Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls` |
| Sessions | restart recovery, branch tree, checkout/backtracking, and manual non-destructive compaction |
| Input | text only |

## Install from a release

Prebuilt binaries are attached to each [GitHub
Release](https://github.com/ch1lam/aice-cli/releases) for macOS (`arm64`,
`amd64`), Linux (`arm64`, `amd64`), and Windows (`amd64`). No Go toolchain is
needed. On first launch AICE detects its helper executables and downloads the
missing ones automatically (see below), so no manual setup is required.

The recommended installer puts AICE in `~/.local/bin` — a user-writable
directory — so `aice update` can later replace the binary in place:

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

A manual download also works. On Apple Silicon (M-series) macOS:

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

Windows (`aice_windows_amd64.zip`):

```powershell
Expand-Archive -Path aice.zip -DestinationPath .
.\aice.exe --version
```

### Updating

`aice update` checks GitHub Releases and replaces the running binary when a
newer version exists; each downloaded asset is verified against the release's
`checksums.txt` before it replaces the executable.

```sh
aice update            # install the newest release
aice update --check    # only report whether an update exists
aice update --force    # reinstall, or install over a dev build
```

In interactive terminals AICE silently checks for a new release at most once a
day and hints `update available: X -> Y (run \`aice update\`)`. Set
`AICE_NO_UPDATE_CHECK=1` to disable that check.

Installs owned by a package manager cannot self-update: `aice update` reports
the manager and asks you to upgrade with it (for example `brew upgrade aice`).


### Helper executables

AICE's `grep` tool needs [`ripgrep`](https://github.com/BurntSushi/ripgrep)
(`rg`), and its `bash` tool needs `bash` (present on macOS/Linux; on Windows
it comes with [Git for Windows](https://git-scm.com/download/win)). On first
launch AICE checks for each helper and downloads it to `~/.aice/bin` when
missing — ripgrep is a small single binary, and Windows Git Bash downloads
the ~60 MB [Portable
Git](https://git-scm.com/download/win) build. Downloads are verified against
a pinned SHA-256 before being used. If a download fails the app still starts,
and the affected tool reports `unavailable` until the helper is installed.

Set `AICE_NO_DEP_INSTALL=1` to disable the automatic downloads and manage
helpers yourself.

On first launch, run `/login` to store your DeepSeek API key.

## Quick start

Requirements:

- the Go version declared in [`go.mod`](./go.mod)
- `bash` and [`ripgrep`](https://github.com/BurntSushi/ripgrep) (`rg`) — the
  app downloads them automatically on first launch if they are missing
- a provider API key (DeepSeek, or OpenCode Go via `OPENCODE_API_KEY`)

```sh
git clone https://github.com/ch1lam/aice-cli.git
cd aice-cli
go build -o ./aice ./cmd/aice
./aice --workspace /path/to/project
```

On first launch, run `/login`, pick a provider, and enter its API key through
hidden input. Keys are stored in `~/.aice/auth.json`. Type `/help` for
commands or `?` for shortcuts.

Run one non-interactive request:

```sh
./aice --workspace . --print "Explain the architecture of this repository."
```

Interactive sessions are created automatically under
`<workspace>/.aice/sessions/`. Use `/session` to see the current file, then
resume it explicitly when needed:

```sh
./aice --workspace . --session .aice/sessions/<session-id>.jsonl
```

Model and reasoning settings live in `~/.aice/settings.json`; credentials are
stored separately. See the [configuration guide](./docs/configuration.md) for
environment variables and interactive commands.

AICE loads project-local prompts (`<workspace>/.aice/SYSTEM.md` and
`APPEND_SYSTEM.md`) only after a project trust decision. The first time you
enter a project with such files, AICE asks whether to trust it; `--approve`
and `--no-approve` make that choice for automated runs, and `/trust` manages
saved decisions. Untrusted projects run with the built-in system prompt and
full host permissions; Project Trust is not a sandbox.

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
