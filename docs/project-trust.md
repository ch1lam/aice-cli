# Project Trust and Prompts

Project Trust decides whether AICE may load project-controlled startup
instructions. It is an input-loading guard, not a sandbox or a per-tool
approval system.

## Protected project files

Only these workspace-relative regular files are protected:

| File | Effect when trusted |
| --- | --- |
| `AGENTS.md` | Appended as project guidance |
| `.aice/SYSTEM.md` | Replaces the base system prompt |
| `.aice/APPEND_SYSTEM.md` | Appended after the base and `AGENTS.md` |

Each file must be valid UTF-8, no larger than 64 KiB, and readable through an
`os.Root` confined to the workspace. `.aice/sessions` does not trigger Trust.

## Decision order

When protected files exist, AICE resolves Trust in this order:

1. `--approve` or `--no-approve` for the current run.
2. The nearest saved directory decision in `~/.aice/trust.json`.
3. `default_project_trust` from `~/.aice/settings.json`.
4. An interactive prompt when the policy is `ask` and a TUI is available.

Non-interactive runs never prompt. With the default `ask` policy and no saved
decision, they ignore project files and continue with global or built-in
instructions.

The startup prompt and `/trust` can save a decision for the workspace or its
parent, or apply a session-only choice. `/trust` affects the next restart; it
does not hot-reload prompt files.

## Prompt assembly

The base prompt is selected from the first available source:

1. Trusted project `.aice/SYSTEM.md`.
2. Global `~/.aice/SYSTEM.md`.
3. The built-in prompt, including the available tool list and working
   directory.

Trusted project `AGENTS.md` is appended next. The append prompt then comes
from trusted project `.aice/APPEND_SYSTEM.md`, otherwise global
`~/.aice/APPEND_SYSTEM.md`.

Compaction always uses its fixed, AICE-owned prompt; project prompt files are
not included in compaction requests.

## `/init`

`/init` asks the current model to inspect the workspace and create or improve
the root `AGENTS.md`. It requires configured credentials. A newly created file
also records the workspace as trusted, and it becomes active after restart.

## What Trust does not do

An untrusted project still runs with the full permissions of the AICE process.
Trust does not restrict file access, `..`, absolute paths, subprocesses,
network access, environment variables, or credentials. Use an external
container, VM, or OS sandbox when isolation is required.
