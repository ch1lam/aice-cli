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
The policies below describe the wired behavior.

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
triggering rule. Grants last for the current Session, across its Agent runs
and provider or credential changes. `/new` clears all dynamic grants, including
when no Session file has been created yet. Configured policies, skill read roots,
and the invocation's `--yolo` setting remain unchanged. Grants never persist to
disk, so resuming a Session in another process starts without them.

The gate checks all applicable policies and extracted paths before returning
an approval list. Any hard denial wins before a prompt is shown. When one call
needs command approval and access to several outside paths, every independent
scope must be approved before the tool executes. Repeated normalized paths
appear once. A directory grant may still leave another already-listed path
to confirm for that call; it applies automatically to later calls. Allow once
approves only the displayed scope for that call and creates no Session grant.
Unknown decisions, empty approval lists on `ask`, invalid replies, and canceled
approval waits fail closed.

| Rule | Options |
| --- | --- |
| `pathAccess.ask` | Allow once; Allow this file for this session; Allow directory `<dir>/` for this session; Deny |
| `permissionGate.dangerous` | Allow once; Allow this exact command for this session; Allow `"<prefix> …"` commands for this session; Deny |
| `unknownTool` | Allow once; Allow tool `"X"` for this session; Deny |
| Other `ask` rules | Allow once; Deny |

The directory option is omitted when the parent is `/` or `$HOME`. The
command-prefix option is omitted when `guard.CommandPrefix` returns empty:
compound commands, dangerous binaries such as `rm`/`sudo`/`dd`/`mkfs`, and
`docker`/`podman` `run`/`create`.

Grant scope within the current Session:

- **file** — that absolute path only
- **directory** — the parent directory and its descendants
- **exact command** — the identical bash command string
- **command prefix** — derived by `guard.CommandPrefix`. Compound commands
  are split with a shell AST (`&&`, `||`, `;`, `|`, and similar). Every
  subcommand must start with an authorized prefix at a word boundary
- **tool name** — that unknown tool name (`AllowToolSession`)

Exact command grants compare the complete original string, including whitespace
and quoting. They are separate from deliberately configured allowed patterns
and prefix grants; an exact grant does not authorize a changed argument or an
additional command, and does not bypass file or path policies.

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

Bash captures combined stdout/stderr within a 50 KiB result limit. Oversized
output retains its beginning and most recent end with an `[output truncated]`
marker between them, followed by exit status or the timeout reason. This keeps
final diagnostics available to the next model request. Capture storage stays
bounded even for a single large write; rendered output is valid UTF-8. Caller
cancellation still stops the process tree and returns cancellation.

## Sessions

Interactive runs create a version 3 JSONL Session under
`<workspace>/.aice/sessions/` when the first prompt is accepted; a process
that never interacts leaves no file behind. An interactive Session that never
records a message or compaction is removed on exit. `--print` creates or resumes a
Session only when `--session` is supplied. An explicit `--session` path is
opened or created during startup, including interactive startup; lazy creation
applies when the interactive path is omitted.

Each file contains a versioned header followed by append-only records:

| Record | Meaning |
| --- | --- |
| `message` | One accepted user message, ended assistant response, or tool result, with its complete metadata |
| `compaction` | A derived summary checkpoint for the active branch |
| `leaf` | A move of the active branch pointer; no history is deleted |

Messages and compactions are tree nodes with stable IDs and parent IDs. Model
context is derived from the active root-to-leaf path. After checkout to a safe
older entry, the next message becomes a new child and creates a branch.

Each source message is appended and synced before the next dependent action.
In particular, assistant tool calls are durable before tools execute, and each
tool result is durable before the next tool runs. Recording failure stops the
run; AICE does not retry persistence from the final result or UI events. If the
live Session has an incomplete tool group, reopen it for recovery or use `/new`
before continuing. Display
failure still permits already-known results to be saved. Usage is counted from
assistant messages and summary checkpoints once, including abandoned branches.

A file can end with an incomplete tool group after interruption. This is a valid
source prefix, but is not usable model context or a checkout target until the
missing results are recovered. Complete user messages and completed assistant
responses without pending tools are safe boundaries. A run or interaction is
not a storage transaction: already saved messages survive later failure.

`/btw` side threads are outside this persistence model. Each new thread
freezes the already accepted context at its first question, then uses that
copy plus its own bounded in-memory history. Side threads run without tools
and never append `message`, `compaction`, or `leaf` records. They also do not
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
recorded messages is left untouched and stays resumable with `--session`.
`/clear` only clears the visible transcript. `/new` does not rebuild the
process environment: prompt files and skill discovery are reused. Dynamic Guard
grants are cleared. Restart AICE to reload prompt files or Skills.

## Recovery and compaction

On open, AICE replays every complete record. It truncates only an incomplete
final JSONL record in a supported v3 file; malformed complete or middle records
fail as corruption. Old versions are rejected before any tail repair, and their
files are neither migrated nor modified. A Session can be resumed only with the
working directory recorded in its header.

Before execution resumes, AICE appends an error result for each outstanding tool
call on the active branch. The result says the outcome is unknown and the tool
may have produced effects; inspect the current state before retrying. Existing
results remain unchanged, recovery is idempotent, and AICE never replays the
interrupted call itself. Tree inspection does not append recovery messages.

Manual compaction summarizes older messages on the active branch and retains
roughly the newest 20,000 tokens (`session.DefaultKeepRecentTokens`). Cuts never split
an assistant tool-call/result group. It appends a checkpoint and never rewrites
source messages or other branches. A single interaction can contain several
safe cuts. If its newest paired group is oversized, AICE may summarize the
completed context while keeping the source messages recoverable. Unanswered
trailing user messages always remain verbatim.

Automatic compaction checks the estimated context before every model request,
including consecutive tool rounds in one interaction. It runs only after all
declared tool results are complete. Initial, steering and follow-up inputs enter
the next request once. Automatic retention is capped at a quarter of the known
model window; a completed group exceeding that cap can be summarized whole.
An oversized unanswered input is preserved and may still prevent continuation.

Stateless `--print` uses the same paired-group selection and summary format in
memory, without creating a Session file. A request gets at most one compaction
attempt followed by a complete budget check. If the summary fails, there is no
safe older history, or the request still does not fit, execution returns an
error; it does not discard source history or repeatedly compact until it fits.

```sh
aice compact --workspace . --session .aice/sessions/<id>.jsonl
```

`/compact` runs the same operation inside the TUI. Compaction uses the selected
provider/model, requires credentials and enough older history, and always uses
an AICE-owned prompt rather than project prompt files. Automatic and manual
compaction share the same provider-neutral, client-generated summary format;
provider-native compaction protocols are not used. Automatic summaries keep the
active run's frozen model and connection settings; manual TUI compaction uses the
current Session selection. A standalone `aice compact` resolves global settings
once, after finding enough history to summarize. If no complete boundary is
available or summary generation fails, AICE preserves the source Session and
returns the error instead of silently dropping history.

A summary request may use the normal provider retry policy. Only its final
natural stop with nonempty text becomes a checkpoint; checkpoint usage includes
all summary attempts that reported usage, including failed attempts. A failed
summary does not append a checkpoint or change source messages.

Print totals include known automatic-summary usage even if the summary or
checkpoint save fails. Durable Session totals, including the TUI and Harbor
projection, count saved source assistants and successful checkpoints only.
Without a saved checkpoint, that summary's usage cannot be recovered after
reopening; AICE does not maintain a separate billing ledger. Missing provider
usage is unknown, not evidence that a request was free.
