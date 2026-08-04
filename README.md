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
| Provider | DeepSeek V4 Flash (OpenAI Responses protocol) and V4 Pro (Anthropic Messages protocol) |
| Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls` |
| Sessions | restart recovery, branch tree, checkout/backtracking, and manual non-destructive compaction |
| Input | text only |

## Quick start

Requirements:

- the Go version declared in [`go.mod`](./go.mod)
- `bash` and [`ripgrep`](https://github.com/BurntSushi/ripgrep) (`rg`) on
  `PATH`
- a DeepSeek API key

```sh
git clone https://github.com/ch1lam/aice-cli.git
cd aice-cli
go build -o ./aice ./cmd/aice
./aice --workspace /path/to/project
```

On first launch, run `/login`. The TUI reads the key through hidden input and
stores it in `~/.aice/auth.json`. Type `/help` for commands or `?` for
shortcuts.

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

## Status

AICE is under active development. The current built-in provider is DeepSeek,
input is text-only, and Session/config formats may evolve before a stable
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
