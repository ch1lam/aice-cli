# Tool Execution and Sessions

## Tool execution boundary

Built-in tools run with the filesystem, process, network, environment, and
credential permissions of the AICE process. Every tool call is checked inline
by the intrinsic execution gate (`internal/guard`) before it runs.
`agent.NewLoop` requires a non-nil `Guard` when the tool set is non-empty
(compaction loops may omit both). Unknown tool names return Decision `ask`
(`RuleID` `unknownTool`); non-interactive runs treat `ask` as `deny`
unless `--yolo` is set.
The `skill` tool is a known tool: it has no path argument and returns
content already parsed at startup, so Check allows it after the known-tool
gate. File policies and path access do not apply to it.
`--workspace` sets the default working directory and is the boundary used by
the path-access gate; it is not a sandbox.

This section is the source of truth for Guard product behavior. Other
documents should link here instead of restating these lists.

### Default wired behavior

`internal/app` constructs the gate through `newExecutionGuard`, passing the
physical workspace and discovered skill directories as `ReadOnlyRoots`.
Other fields use `DefaultConfig`; `config.Settings` has no guard field.
The policies below describe the intended wired behavior. Known implementation
discrepancies in approval scope and rule evaluation are tracked in
[Maintenance](maintenance.md#guard-approval-behavior).

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
   `.env.*` glob. The default existence check applies the rule to existing
   paths; unresolved expansions are handled conservatively. `noAccess` blocks
   path-bearing tools when the rule matches an extracted target.
3. **Path access** — `pathAccess.mode` defaults to `ask` for paths outside
   the workspace. Mode values are `allow` / `ask` / `block`. Default
   `allowedPaths` is empty.

Each check returns Decision `allow` / `ask` / `deny`. Non-interactive
`--print` runs treat `ask` as `deny` (fail-closed) unless `--yolo` is
set. `--yolo` upgrades every `ask` to `allow` and skips the interactive
confirmation prompt. It does not lift `deny`: `permissionGate.autoDeny`
still always wins, and file-policy `noAccess` / `readOnly` denials are
unchanged. Interactive `ask` prompts generate options from the
triggering rule. The intended grant lifetime is the current Session, across
its Agent runs;
`/new` must clear grants and none persist to disk. The current menu still says
“for this run” and the implementation retains grants until process exit.
This confirmed lifecycle correction is pending; see
[Maintenance](maintenance.md#guard-approval-behavior).

| Rule | Options |
| --- | --- |
| `pathAccess.ask` | Allow once; Allow this file for this run; Allow directory `<dir>/` for this run; Deny |
| `permissionGate.dangerous` | Allow once; Allow this exact command for this run; Allow `"<prefix> …"` commands for this run; Deny |
| `unknownTool` | Allow once; Allow tool `"X"` for this run; Deny |
| Other `ask` rules | Allow once; Deny |

The directory option is omitted when the parent is `/` or `$HOME`. The
command-prefix option is omitted when `guard.CommandPrefix` returns empty:
compound commands, dangerous binaries such as `rm`/`sudo`/`dd`/`mkfs`, and
`docker`/`podman` `run`/`create`.

Intended grant scope within the current Session (current menu labels above):

- **file** — that absolute path only
- **directory** — the parent directory and its descendants
- **exact command** — the identical bash command string
- **command prefix** — derived by `guard.CommandPrefix`. Compound commands
  are split with a shell AST (`&&`, `||`, `;`, `|`, and similar). Every
  subcommand must start with an authorized prefix at a word boundary
- **tool name** — that unknown tool name (`AllowToolSession`)

`permissionGate.autoDeny` always wins and cannot be bypassed by any grant.
Path grants of `/` or the user's home directory are ignored, so a
Session-scoped grant cannot authorize the entire filesystem or home
directory. Deny may include an optional user note; the loop appends it to
the paired error tool result as `User feedback: ...`. A reply `OptionID`
that was not offered for that prompt is treated as Deny. Stronger
isolation still belongs to an external container, VM, or OS sandbox.

Project Trust does not change tool permissions; see [Project Trust and
prompts](project-trust.md).

### Engine-only configuration

`guard.Config` can set `enabled`, `applyBuiltinDefaults`, `policies`
(including Protection `readOnly`/`noAccess`, glob/regex matching, and
`onlyIfExists`), `permissionGate` (`patterns`, `customPatterns`,
`allowedPatterns`, `autoDenyPatterns`, `requireConfirmation`),
`pathAccess` (`mode`, `allowedPaths` with `~` expansion), and
`readOnlyRoots`. `readOnly` blocks `write`/`edit`/`bash`; `noAccess` blocks
path-bearing tools (`read`/`write`/`edit`/`bash`/`grep`/`find`/`ls`). The
`skill` tool has no path argument, so file policies do not apply to it.
`readOnlyRoots` are constructor-configured directories (and descendants)
that skip path-access ask/block for `read`/`grep`/`find`/`ls`; `write`/`edit`
are not granted. Callers pass discovered skill directories; see
[Skills](architecture.md#skills). These fields are engine-only, not yet
wired to `settings.json`. Do not document them as user-facing product
settings until `internal/app` loads them.

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

Interactive runs create a version 2 JSONL Session under
`<workspace>/.aice/sessions/` when the first prompt is accepted; a process
that never interacts leaves no file behind. An interactive Session that never
records a turn or compaction is removed on exit. `--print` creates or resumes a
Session only when `--session` is supplied. An explicit `--session` path is
opened or created during startup, including interactive startup; lazy creation
applies when the interactive path is omitted.

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
`/checkout`. `/new` detaches from the current Session without creating a
file; the next accepted prompt starts a fresh one. A previous file that
recorded turns is left untouched and stays resumable with `--session`.
`/clear` only clears the visible transcript. `/new` does not rebuild the
process environment: prompt files, skill discovery, and Guard grants are
currently reused. See [known lifecycle discrepancies](maintenance.md#startup-state-and-new).

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
