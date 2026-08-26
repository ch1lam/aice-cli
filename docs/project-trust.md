# Project Trust and Prompts

Project Trust decides whether AICE may load project-controlled startup
instructions. It is an input-loading guard, not a sandbox. Per-tool approval
is handled by the intrinsic execution gate (`internal/guard`); see
[Tool execution and Sessions](execution-sessions.md#tool-execution-boundary).

## Protected project resources

Only these workspace-relative resources are protected:

| Resource | Type | Effect when trusted |
| --- | --- | --- |
| `AGENTS.md` | regular file | Appended as project guidance |
| `.aice/SYSTEM.md` | regular file | Replaces the base system prompt |
| `.aice/APPEND_SYSTEM.md` | regular file | Appended after the base and `AGENTS.md` |
| `.agents/skills/` | directory | Project-level Agent Skills are discovered and listed in the system prompt. See [Skills](architecture.md#skills). |

The three prompt files must be regular files, valid UTF-8, no larger than
64 KiB, and readable through an `os.Root` confined to the workspace. Those
read constraints are specific to the prompt files; general file tools
(`read`, `edit`, `bash`, ...) are not confined by `os.Root` and open paths
with the full permissions of the AICE process, inside or outside the
workspace. `.aice/sessions` does not trigger Trust.

`.agents/skills/` is gated by existence only: an empty directory still
triggers Trust, and a regular file of the same name does not. Discovery
inspects the path through `os.Root`, so a symlink cannot escape the
workspace. `SKILL.md` parse and size constraints are in
[Skills](architecture.md#skills).

## Decision order

When protected resources exist, AICE resolves Trust in this order:

1. `--approve` or `--no-approve` for the current run.
2. The nearest saved directory decision in `~/.aice/trust.json`.
3. `default_project_trust` from `~/.aice/settings.json`.
4. An interactive prompt when the policy is `ask` and a TUI is available.

Non-interactive runs never prompt. With the default `ask` policy and no saved
decision, they ignore project resources and continue with global or built-in
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
`~/.aice/APPEND_SYSTEM.md`. When the merged skill catalog is non-empty, a
list of skill names and descriptions is appended last so a custom `SYSTEM.md`
still receives it. Skill bodies are not injected; they load on demand through
the `skill` tool. See [Skills](architecture.md#skills).

Compaction always uses its fixed, AICE-owned prompt; project prompt files are
not included in compaction requests.

## `/init`

`/init` asks the current model to inspect the workspace and create or improve
the root `AGENTS.md`. It requires configured credentials. A newly created file
also records the workspace as trusted, and it becomes active after restart.

## What Trust does not do

An untrusted project still runs with the full permissions of the AICE process
except for the intrinsic execution gate. Trust does not restrict file access,
`..`, absolute paths, subprocesses, network access, environment variables, or
credentials — that is the role of `internal/guard` (file policies, permission
gate, path access) and, for strong isolation, an external container, VM, or OS
sandbox.

The `os.Root` confinement of protected prompt files is a loading guard, not a
general file-access boundary: it exists so untrusted content cannot influence
the prompt, and it never applies to file tools. File access boundaries belong
to the execution gate and to the externally selected execution environment.
