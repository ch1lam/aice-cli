# Tool Execution and Sessions

## Tool execution boundary

Built-in tools run with the filesystem, process, network, environment, and
credential permissions of the AICE process. Every tool call is checked inline
by the intrinsic execution gate (`internal/guard`) before it runs.
`agent.NewLoop` requires a non-nil `Guard` when the tool set is non-empty
(compaction loops may omit both). Unknown tool names return Decision `ask`
(`RuleID` `unknownTool`); non-interactive runs treat `ask` as `deny`.
`--workspace` sets the default working directory and is the boundary used by
the path-access gate; it is not a sandbox.

This section is the source of truth for Guard product behavior. Other
documents should link here instead of restating these lists.

### Default wired behavior

`internal/app` always constructs the gate with `guard.New(workspace,
guard.Config{})`. An empty `Config` is merged with `DefaultConfig`.
`config.Settings` has no guard field, so the following is what every current
build actually enforces.

The gate evaluates three layers in order:

1. **Permission gate (`bash`)** — structural dangerous-command detection.
   Built-in matchers require confirmation for shapes such as `rm -rf`, `sudo`,
   `dd of=`, `mkfs.`, `chmod -R 777`, `chown -R`, `shred`, `wipefs`,
   `blkdiscard`, `fdisk`/`parted`, `doas`/`pkexec`, and container
   `docker`/`podman` with subcommand `run` or `create` plus `--privileged`
   (example: `docker run --privileged`). Default `autoDenyPatterns` is empty.
2. **File policies** — default rule `secret-files` uses Protection
   `noAccess`. Protected basenames: `.env`, `.env.local`, `.env.production`,
   `.env.prod`, `.dev.vars`. Allow-listed: `*.example.env`, `*.sample.env`,
   `*.test.env`, `.env.example`, `.env.sample`, `.env.test`. There is no
   `.env.*` glob. `noAccess` blocks all tools.
3. **Path access** — `pathAccess.mode` defaults to `ask` for paths outside
   the workspace. Mode values are `allow` / `ask` / `block`. Default
   `allowedPaths` is empty.

Each check returns Decision `allow` / `ask` / `deny`. Non-interactive runs
treat `ask` as `deny` (fail-closed). The interactive TUI maps `ask` to
`Allow once` / `Allow always` / `Deny`. `Allow always` is current-run,
memory-only: grants live in `sessionAllowed` / `sessionAllowedPaths` and
never persist to disk. Path grants of `/` or the user's home directory are
ignored, so `Allow always` cannot authorize the entire filesystem or home
directory. Stronger isolation still belongs to an external
container, VM, or OS sandbox.

Project Trust does not change tool permissions; see [Project Trust and
prompts](project-trust.md).

### Engine-only configuration

`guard.Config` can set `enabled`, `applyBuiltinDefaults`, `policies`
(including Protection `readOnly`/`noAccess`, glob/regex matching, and
`onlyIfExists`), `permissionGate` (`patterns`, `customPatterns`,
`allowedPatterns`, `autoDenyPatterns`, `requireConfirmation`), and
`pathAccess` (`mode`, `allowedPaths` with `~` expansion). `readOnly` blocks
`write`/`edit`/`bash`; `noAccess` blocks all tools. These fields are
engine-only, not yet wired to `settings.json`. Do not document them as
user-facing product settings until `internal/app` loads them.

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

`/btw` side threads are outside this persistence model. Each new thread
freezes the already accepted context at its first question, then uses that
copy plus its own bounded in-memory history. Side threads run without tools
and never append `turn`, `compaction`, or `leaf` records. They also do not
contribute to main Session usage totals or compaction input. Limits, idle
windows, and TUI controls are documented in
[Configuration](configuration.md#btw). Closing a panel only hides the thread;
Ctrl+D ends it. Exiting AICE discards every side thread, so none can be
recovered by resuming the main Session.

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
roughly the newest 20,000 tokens (`session.DefaultKeepRecentTokens`). It
appends a checkpoint and never rewrites source turns or other branches.
Automatic compaction runs before the first model request of a new interaction
when the estimated context crosses the model's reserved-token threshold; a
queued follow-up is compacted only after the preceding interaction has
reached its complete-turn boundary. If the newest complete turn is itself too
large to retain, AICE summarizes the entire active branch rather than failing
solely because that one turn is oversized; source turns remain recoverable in
the Session.

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
