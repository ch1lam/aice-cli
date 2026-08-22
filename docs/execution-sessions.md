# Tool Execution and Sessions

## Tool execution boundary

Built-in tools run with the filesystem, process, network, environment, and
credential permissions of the AICE process, but every tool call is checked
inline by the intrinsic execution gate (`internal/guard`) before it runs.
`--workspace` sets the default working directory and is the boundary used by
the path-access gate; it is not a sandbox.

The gate is enabled by default and evaluates three layers in order:

1. **Permission gate (bash)** — structural dangerous-command detection and
   substring/regex patterns; `autoDeny` is always denied, built-in dangerous
   commands (`rm -rf`, `sudo`, `dd of=`, `mkfs`, `chmod -R 777`, `chown -R`,
   `shred`, `wipefs`, `blkdiscard`, `fdisk`/`parted`, `docker --privileged`,
   `doas`/`pkexec`, …) require confirmation unless allow-listed.
2. **File policies** — glob-protected paths (default `secret-files`: `.env`,
   `.env.local`, `.env.*`, `.dev.vars` with `noAccess`; `*.example.env` etc.
   are allow-listed) with `noAccess`/`readOnly` enforcement; `readOnly` blocks
   `write`/`edit`/`bash`, `noAccess` blocks all tools. Matching is
   glob-aware (`/` distinguishes full-path vs basename, `**` supported) with
   optional `regex` opt-in, and `onlyIfExists` probes.
3. **Path access** — access outside the workspace (`mode: ask` by default)
   requires confirmation or is blocked (`allow`/`block`); per-file and
   per-directory `allowedPaths` and `~` expansion are supported.

Each check returns `allow` / `ask` / `deny`. Non-interactive runs treat `ask`
as `deny` (fail-closed); the interactive TUI maps `ask` to a confirmation
prompt (`Allow once` / `Allow always for this run` / `Deny`). `Allow always`
grants are memory-only for the current run and never persist to disk.

The tool layer still enforces correctness and resource safety:

- validate arguments and malformed paths before side effects;
- propagate cancellation and terminate spawned process trees;
- bound time, stdout, stderr, and captured output;
- preserve exit status and distinguish command failures from tool failures;
- pair every tool call with one result;
- keep credentials and prompt content out of logs.

Structured subprocesses use executable/argument separation. The `grep` tool
invokes `rg` with `--` before model-controlled pattern/path values. The `bash`
tool intentionally crosses a shell boundary and applies the same timeout,
output, cancellation, and process-tree controls.

Use an external container, VM, or OS sandbox when stronger isolation is
required. Project Trust does not change tool permissions (it only gates prompt
loading); see [Project Trust and prompts](project-trust.md). For the gate's
implementation and pattern semantics, see `internal/guard`.

## Sessions

Interactive runs automatically create a version 2 JSONL Session under
`<workspace>/.aice/sessions/`. `--print` creates or resumes a Session only when
`--session` is supplied.

Each file contains a versioned header followed by append-only records:

| Record | Meaning |
| --- | --- |
| `turn` | One complete user interaction, including in-interaction steers, paired tool calls/results, and usage |
| `compaction` | A derived summary checkpoint for the active branch |
| `leaf` | A move of the active branch pointer; no history is deleted |

Turns and compactions are tree nodes with stable IDs and parent IDs. Model
context is derived from the active root-to-leaf path. After checkout to an
older entry, the next complete turn becomes a new child and creates a branch.

A Session turn is a persistence boundary, not an Agent-run boundary. One
active Agent run may contain an initial interaction followed by any number of
queued follow-up interactions. AICE appends each interaction immediately when
the Agent reaches its natural stop boundary, while the same run remains active
to poll follow-up input. Cancellation or failure appends the unfinished final
interaction as a terminal turn so completed work is not lost.

`/btw` side threads are explicitly outside this persistence model. Each new
thread freezes the already accepted context at its first question, then uses
that copy plus its own bounded in-memory history. Side threads run without
tools and never append `turn`, `compaction`, or `leaf` records. They also do
not contribute to main Session usage totals or compaction input.

AICE keeps at most five live side threads and 20 interactions per thread. A
thread becomes read-only after 20 minutes without a completed, cancelled, or
failed answer and is permanently deleted after 120 minutes of inactivity.
Closing its panel only hides it; Ctrl+D explicitly ends it. Exiting AICE
discards every side thread immediately, so none can be recovered by resuming
the main Session.

## Resume and navigate

```sh
# Resume in the interactive TUI
aice --workspace . --session .aice/sessions/<id>.jsonl

# Inspect all branches; * is the active leaf and + is its ancestry
aice session tree --workspace . --session .aice/sessions/<id>.jsonl

# Move the active leaf without deleting later history
aice session checkout --workspace . \
  --session .aice/sessions/<id>.jsonl --entry <entry-id>

# Move back to the tree root
aice session checkout --workspace . \
  --session .aice/sessions/<id>.jsonl --entry root
```

The TUI exposes the same behavior through `/session`, `/tree`, and
`/checkout`. `/clear` only clears the visible transcript.

## Recovery and compaction

On open, AICE replays every complete record. It truncates only an incomplete
final JSONL record; malformed complete or middle records fail as corruption.
A Session can be resumed only with the working directory recorded in its
header.

Compaction summarizes older complete turns on the active branch and retains
roughly the newest 20,000 tokens. It appends a checkpoint and never rewrites
source turns or other branches. Automatic compaction runs before the first
model request of a new interaction when the estimated context crosses the
model's reserved-token threshold; a queued follow-up is compacted only after
the preceding interaction has reached its complete-turn boundary. If the
newest complete turn is itself too large to retain, AICE summarizes the entire
active branch rather than failing solely because that one turn is oversized;
source turns remain recoverable in the Session.

```sh
aice compact --workspace . --session .aice/sessions/<id>.jsonl
```

`/compact` runs the same operation inside the TUI. Compaction uses the selected
provider/model, requires credentials and enough older history, and always uses
an AICE-owned prompt rather than project prompt files. Automatic and manual
compaction share the same provider-neutral, client-generated summary format;
provider-native compaction protocols are not used. If no complete boundary is
available or summary generation fails, AICE preserves the source Session and
returns the error instead of silently dropping history.
