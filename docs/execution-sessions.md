# Tool Execution and Sessions

## Run resource limits

AICE has no model-round or tool-step ceiling by default. Optional
`--run-token-budget N` and `--run-timeout 30m` bound one Agent run; both default
to zero (unlimited). They work in interactive and print modes, with the same
configuration precedence as model settings. See [configuration](configuration.md#run-limits).

A run includes its steering and queued follow-up inputs. Automatic compaction
is charged to the same run. A new user-triggered run starts a fresh budget,
including when continuing an existing Session; historical usage is not charged
again. These are run controls, not a persistent Session or Goal budget.

Token accounting uses provider-reported total tokens, including cached tokens;
when total is absent it sums normalized input, output, cache-read and cache-write
counts without counting reasoning twice. Reported failed attempts count too.
Missing usage is not estimated, so providers that omit it cannot be fully bounded
by tokens; use a timeout as well. Checks occur before model requests and tools,
not while individual tokens stream. A response or in-flight compaction operation
can overshoot the budget. This is not an exact billing cap. A final answer that
already completed without tools remains successful.

Timeouts include model requests, retries, tools, approval waits and compaction.
Providers and tools must observe context cancellation; cleanup may take additional
time. A caller's earlier deadline or cancellation keeps its original meaning.

On exhaustion the Loop stops new work, records known outcomes and paired error
results for skipped calls, then records the stop reason without another model
request. Print mode exits nonzero for a resource stop; interactive mode returns
control to the user. Completed edits are retained. Send a new message to continue
with a fresh run budget, or restart with different limits. This does not bypass
Guard or Project Trust.

### Optional turn limit

`--max-turns N` optionally bounds model request attempts in each Agent run.
The default `0` is unlimited; positive integers set a ceiling. Each call to the
model counts once, including failed attempts and retries. Tool calls do not count
separately, and a synthetic terminal message does not consume another turn.

The last permitted model response's valid tool batch executes normally through
Guard, subject to token/time budgets and cancellation. If continuation would
require another model request, the Loop records `maximum model turns reached`
and stops without requesting a final summary. A final answer at the limit still
succeeds if no steering or follow-up needs another model request. Print mode
exits nonzero for a limit stop; interactive mode returns control to the user.
Completed actions and accepted inputs remain in history.

Steering and queued follow-ups share the count; a new run starts fresh. Automatic
compaction does not reset it. Summary generation uses separate loops with their
own turn counters; it does not consume the main run's turn allowance. No new
compaction starts once the main turn limit has been reached. Use token/time
budgets as well to bound resources across compaction and the main run.

### Repeated-tool detection

By default, AICE stops after 8 consecutive completed tool rounds have identical
ordered tool names, arguments and results. Configure `--run-no-progress-limit N`
with `N >= 2`, or `0` to disable it. JSON object key order and whitespace do not
matter; different call IDs, timestamps or assistant commentary alone do not
count as progress. Result content, error status, diff, truncation and structured outcome details
participate in the comparison. Web evidence metadata does not. Repeated denials are detected too.

Changed tool work resets the streak. Accepted steering and natural completion
also reset it, so queued follow-ups and new runs start fresh. Compaction does
not reset it. This counter is separate from the resource budget, which remains
shared across steering and follow-ups in the same run.

Detection happens after the whole tool round completes: actual outcomes are
retained, then a terminal reason is recorded without another model request.
Print mode exits nonzero; interactive mode returns control to the user. Review
the results and adjust the instruction or threshold before continuing. No work
is rolled back, and this stop is not automatically retried.

This is a conservative repetition heuristic, not a semantic progress evaluator.
Alternating cycles and commands whose output keeps changing can escape it;
legitimate polling that repeatedly returns the same result can trigger it.
For polling tasks, adjust or disable detection and consider a timeout. The
check does not impose a maximum on productive model rounds or tool calls.

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
`tool_search`, `mcp_resource_list` and `mcp_server_info` are known discovery tools.
Their application catalog uses only explicitly supplied service bindings and
checks current eligibility. The lazy connection owner requires a separate saved
connection approval or ordinary yolo Ask bypass before connecting. Discovery and
reading initialization instructions grant no execution rights; selected MCP
tools and resource readers still cross the identity-bound Guard. Server text is
source-tagged untrusted data, never a policy update. CLI and interactive management
publish connection changes and revocation; see
[MCP](mcp.md#run-catalog-and-tool-search).
`web_search` and `web_fetch` are known tools that pass through the same
execution gate without a web confirmation step: `web_search` allows when a
search service is bound (instance ID plus endpoint origin, set by the
application per run; without a binding it denies) and `web_fetch` allows
when the URL passes the shared URL-shape check (malformed URLs, userinfo,
zone-scoped IPv6 and non-default ports deny). Address validation, redirects
and body limits run inside the tool, outside the Guard lock. See [Web search
and fetch](web.md#permissions).
Enabled Computer Use contributes the application-owned `managed:cua` service,
not the legacy `desktop_*` tools. Discovery requires a live desktop Run binding;
selected native operations pass the same identity-bound MCP Guard. Its reviewed
operation inventory inherits the Computer Use preference without a separate
per-application approval. Explicit deny, configured restrictions and revoked
bindings still win under `--yolo`. Ordinary services cannot obtain this authority
from their name or configuration. Native admission separately checks standard
mode, identity and OS grants; tool calls cannot install or authorize the helper.
Workspace file policies do not constrain GUI actions in other applications.
The old `desktop_*` tools and their Guard toggle have been removed. Those names
follow ordinary unknown-tool policy and have no executable registration. See
[Computer Use integration status](desktop.md).
`request_user_input` is a known interactive-only tool: the gate allows it
without an extra confirmation step, and answers never change the
authorization scope (`--yolo` never answers for the user). The tool is
registered only for interactive runs; `--print`, Harbor, and `/btw` never
see its schema, so a model without a frontend states the missing
information in its output instead of waiting. Answers travel on a dedicated
reply channel, never the steering mailbox, and each prompt accepts exactly
one submission bound to its tool-call ID: skips stay skips, late or
duplicate replies are dropped, and cancellation wins over a racing submit.
`--workspace` sets the default working directory and is the boundary used by
the path-access gate; it is not a sandbox.

Browser commands pass through the same bash gate. It can check command strings
and screenshot paths, but does not enforce website action or domain isolation.
Browser skills provide behavioral guidance rather than a security boundary; see
[Browser automation](browser.md#screenshots-and-authority).

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
| `mcp.tool` (bound integration) | Allow once; Allow current tool version for this Session; Allow current service tool versions for this Session; Deny |
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
- **bound MCP tool/service** — source, service, connection, effective permission
  scope and current tool schema versions; generic name grants do not apply.
  The catalog, Guard and approval bridge enforce this boundary in main runs.
  Connection approval grants no tool permissions. Explicit cross-Session user
  rules are saved separately through MCP management, bound to the reviewed
  connection, configured scope, operation and schema; Session prompts never save
  them. User denies survive yolo. See [MCP permission](mcp.md#bound-execution-permission).

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

The tool layer validates arguments before effects, propagates cancellation,
bounds output and subprocess lifetime, preserves exit status, and keeps
credentials/prompt content out of logs.

- `write` requires explicit string `content`; an empty string is valid. It
  replaces complete content, creating parents when needed. Omitted/null/non-string
  content fails before mutation. Use `edit` for partial changes.
- `edit.edits` is a nonempty array of explicit `oldText`/`newText` strings;
  `oldText` cannot be empty and empty `newText` deletes. Unknown/legacy fields
  and malformed entries fail before file access. All matches use the same
  original file, must be unique and non-overlapping, with BOM preservation and
  CRLF/LF normalization only. Errors identify original entry indices. A final
  byte-identical result is a no-change error. Matching failures require rereading
  and correcting the edit, not blindly overwriting the file.
- `write`/`edit` serialize mutations per Workspace through cleanup. They check
  cancellation before temporary-file preparation and rename. Before commit,
  cancellation preserves the original and removes temporary files; created parent
  directories can remain. Rename's actual outcome wins once commit starts.
  Known success is recorded even if the run then cancels. This is not atomic
  cancellation/rename or process-crash recovery.
- `read` text offsets are 1-based. Empty files permit offset 1; past-EOF offsets
  fail with the known line count, without an extra scan. A final newline creates
  no extra content line. Complete-line output is bounded to 2000 lines / 50 KiB
  including continuation notices. An oversized first line calls for bash.
  Explicit smaller pages may count remaining lines within the existing bound;
  default/byte-limited pages do not scan the rest for totals.
- `read.image_id` accesses only images retained on the active Session ancestry,
  including before compaction; unknown IDs fail and no host path grant is created.
  Stateless print retains images only for that invocation. File reads still cross
  Guard. [Image inputs](configuration.md#clipboard-images) documents formats/crops.
- `grep` invokes ripgrep with separated executable/arguments and `--` before
  model pattern/path values. Context reads are capped at 10 MiB per file; missing,
  changed or oversized files retain the original matching text and an explanation.
  Per-call caching is bounded to 128 files / 16 MiB; uncached reads still obey the
  file cap. It is not a filesystem snapshot or a bound on all temporary decoding
  allocations. [Truncation metadata](contracts.md#tool-truncation-metadata) distinguishes
  match, byte and long-line limits.
- `bash` deliberately crosses a shell boundary. Its 50 KiB combined output keeps
  the beginning and most recent end with a truncation marker, plus exit/timeout
  status. Cancellation terminates its process tree. On Windows native Bash uses
  a Job Object; optional WSL uses a Linux process group and stdin lifetime channel,
  with bounded launcher cleanup, without shutting down the distribution. See
  [runtime helpers](installation.md#runtime-helpers).

### Directory listings

`ls` lists one directory, including dotfiles. Directory names end in `/`;
symlinks, including dangling links, end in `@`. Paths retain literal spelling
and resolve relative to the physical workspace. An empty directory returns
`(empty directory)`. These output conventions are included in the tool description.

All directory entries are read before sorting names in case-sensitive byte order
and applying the requested limit. Each result contains a prefix of that global
name order; the number returned also depends on the byte budget and reserved
notices. The output limits do not bound directory-read memory or sorting work: memory grows with the directory size, and sorting takes
O(n log n) comparisons. Listing is not an atomic filesystem snapshot. Cancellation
is checked after the directory read and while formatting entries; the synchronous
host directory read and sort are not interrupted midway.

The `ls` tool keeps complete directory entries within its 50 KiB output budget,
including truncation notices. It reserves notice space before adding entries;
filenames, directory suffixes, and symlink suffixes are never partially emitted.
Entry-count and byte-limit notices can both appear when both bounds apply.

`limit` defaults to 500 and has a hard maximum of 500. Values above 500 are
rejected with guidance instead of silently reduced. Negative values are rejected;
explicit zero retains the default for compatibility, while the model-facing
schema advertises 1–500. When a smaller entry limit is reached, the result suggests
a larger limit up to 500. At the hard maximum or byte limit, use `find` to filter
names or `bash` to inspect the directory in batches; `ls` has no pagination.

### Write paths and atomic replacement

`write` resolves literal relative paths from the physical workspace. It follows
existing file symlinks and atomically replaces the final regular target file,
preserving the link itself and the target's permission bits through the existing
temporary-file/rename path (host umask still applies). Relative link targets,
link chains, and parent directory links are resolved before processing subsequent
`..` components. New files and missing parent directories are still created with
the existing modes. Dangling links, link cycles, inaccessible components,
non-directory parents, trailing separators, and traversal through missing
directories fail before creating directories or temporary files.

The application Guard adapter checks both the requested path and the physical
mutation destination using `Write.ResolvePath` or `Edit.ResolvePath`, so protected
targets and paths outside the workspace retain their policy and approval requirements, including
under `--yolo`. File-policy existence checks use the action path, not its
shortened match/display spelling, so a literal workspace `~` directory cannot
redirect the existence probe to the home directory. The same resolver runs inside
each tool's mutation lock. A call-local Guard revalidation rejects a changed
destination after approval waits; the caller must retry for a fresh permission check. These checks are not a filesystem
snapshot: external changes between revalidation, resolution, and rename remain
subject to host isolation. No file descriptors pin directory identity.

`edit` uses the same physical path resolution and atomic replacement boundary,
preserving file and parent directory links. It requires an existing regular
target and never creates a missing file or follows a dangling link to create one.
Its Guard checks and post-approval revalidation also require that target to remain
an existing regular file. Atomic commit and cancellation semantics are unchanged.
`atomicWrite` is shared by both tools and does not resolve links itself.

### Grep path spelling

Grep shares read's basic input normalization: strip one leading `@`, expand `~`
or `~/`, and fold the same Unicode spaces to ASCII spaces. On Windows native
separators are accepted. Use `./@name` or `./~/name` for literal workspace names.
It does not probe read's screenshot, NFD or apostrophe filename variants.
Relative paths start at the physical workspace, and symlinks resolve before
parent traversal. Ripgrep receives the physical target; file results use that
target's basename and directory results use paths relative to that target.

The application Guard adapter checks the original input, normalized absolute
spelling and physical target before any approval. A denial on any spelling wins,
including with `--yolo` or existing path grants. Both permission checking and
execution use `Workspace.ResolveGrepPaths`; a changed target during approval is
rejected by the existing pre-execution revalidation hook. This checks the search
root, not each recursively discovered file, and is not an atomic filesystem
snapshot or a replacement for host isolation. Missing or unresolvable roots fail.

### Read path spelling

`internal/tool` owns read-only spelling tolerance. It strips one leading `@`,
expands `~` or `~/`, and folds U+00A0, U+2000–U+200A, U+202F, U+205F and U+3000
to ASCII spaces. It does not trim whitespace or apply compatibility normalization.
Relative paths resolve from the physical workspace; absolute paths and parent
traversal remain subject to Guard access checks.

The first existing candidate wins, in this order:

1. The resolved path after input normalization (including space folding).
2. That path with the space before `AM.` or `PM.` replaced by U+202F,
   case-insensitively.
3. The normalized base in Unicode NFD (canonical decomposition and combining-mark
   ordering across scripts).
4. The normalized base with straight apostrophes replaced by U+2019.
5. NFD plus the apostrophe replacement.

These are variants of the same base, not an exhaustive combination search.
Space folding occurs before probing: when both ASCII-space and Unicode-space
names exist, the ASCII-space name wins. An existing base wins over all fallback
names, even if opening it fails or its permissions deny access. No lower-priority
candidate is retried after selection. If none exists, read reports the missing
normalized base. Normalization-insensitive filesystems can satisfy the base
lookup with a canonically equivalent name; tests use byte-exact probes to verify
NFD and candidate conflicts independently of macOS filesystem behavior.

The application Guard adapter checks both the requested name and the selected
physical target from `Read.ResolvePath`, including symlink targets, before any
approval. Read execution and file attachments use the same input spelling and
resolver; attachments use the physical path only for identity and display, without
normalizing it again as user input. Fallback selection never skips a policy denial.
This is a path check, not an atomic filesystem snapshot: external filesystem changes between checking and opening remain subject to the
host isolation boundary. `write` and `edit` keep literal workspace resolution;
these spelling fallbacks do not grant mutation access.

`write` creates new files or atomically replaces the complete content of an
existing file, automatically creating missing parent directories. Relative paths
resolve from the working directory; mutation paths retain literal spelling.
The TUI shows a bounded [content preview](configuration.md#interactive-input-delivery)
from tool arguments, independently of execution and its result.

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
| `title` | A session-wide display title; empty text restores the automatic title |

Title changes append to the original JSONL file; there is no separate metadata
log and no rewrite of the header or source messages. The last `title` record
wins across all branches. Titles have unique record IDs but are not tree nodes,
do not move the active leaf, and never enter model context. Text is normalized
to single spaces and limited to 200 Unicode characters without control characters.
New and existing v3 files use the same display rule: prefer the latest title;
if it is absent or empty, use the first user request. No migration or separate
legacy-reader path is needed. If neither contains displayable text, use the
session filename stem.

Tool result metadata (evidence, truncation, diffs, structured outcomes and image
originals) stays in the same additive v3 source records. Old records gain no
inferred metadata, live capability or authorization. Protocol and TUI views are
derived without rewriting source; [Runtime contracts](contracts.md#messages-and-model-boundary)
own validation and cloning. `tool_result_read` accesses retained results on the
active ancestry, including before compaction, without re-execution; see
[MCP readback](mcp.md#model-views-and-result-readback).

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

Store pairing state and published context are derived caches. New records become
visible only after sync. App publishes only complete message groups; checkout
and compaction rebuild context. Source copies clone mutable slices, images and
arguments. Metadata-only queries do not copy the transcript. Implementation
owners are [Store](../internal/session/store.go) and
[conversation](../internal/app/conversation.go).

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

Use `/history` or Ctrl+R while idle to browse regular `.jsonl` files under
`<workspace>/.aice/sessions/`. The picker validates workspace identity, hides empty
Sessions, groups by local calendar date and sorts by recorded activity. Invalid
files remain visible as unavailable. Explicit `--session` can open files outside
this directory; `/history <id>` restores an existing local filename stem and
never creates a missing Session.

| Control | Action |
| --- | --- |
| Up/Down, click | Select Session/group; Enter restores or folds a group |
| `/` | Focus search; search matches title, filename stem and user/assistant prose across branches |
| Right / Left | Open/focus preview / return to list |
| Mouse wheel | Scroll the pane under the pointer without changing keyboard focus |
| F2 | Rename; Enter saves, empty restores automatic title, Esc cancels |
| F4 | Read-only transcript at a match or latest conversation |
| T in reader / Ctrl+T in idle main view | User-question directory |
| End in reader | Latest active-branch conversation |
| Enter in reader | Restore saved active branch |
| Esc | Return from reader; hide preview; then close picker |

Search does not index tool payloads, reasoning or image bytes. Other-branch
matches are labeled; selecting one still restores the saved active branch.
Scanning/previewing is cancellable, batches preserve selection, and the derived
prose cache is bounded to 256 Sessions / 32 MiB. File identity/size/mtime changes
invalidate it. There is no durable secondary search index.

Renames append a title record, count as activity and preserve file identity.
They use the current writer or temporarily lock the selected file; busy files
fail. A rename does not repair an incomplete JSONL tail or recover pending tools;
resume first. Closing the editor cannot undo a committed save.

Switching requires main and BTW responses to be idle. App validates/prepares the
target before replacement; failures keep the old Session and draft. Success
clears BTW threads and temporary Guard grants, rotates browser state, and keeps
current model/provider/thinking preferences. It never rolls back workspace files.
The display restores original branch records with details folded; model context
independently uses compaction checkpoints. Read-only inspection never writes a
checkout; `/checkout` explicitly moves the active leaf. Code-fold/copy controls
are described in [Configuration](configuration.md#tool-output-and-code-panels).

Listing and preview use read-only replay bounded by the file's initial size;
they never truncate incomplete tails or synthesize interrupted tool results.
Writable opens acquire a nonblocking OS lock before replay or recovery and
hold it until close. Unix writers explicitly unlock before closing the owning
descriptor, so a concurrently forked child cannot delay release until exec.
A second writer fails with a busy-session message while
read-only browsing remains available. The OS releases the lock when the process
exits; no sidecar lock file is used. Platforms without a supported OS lock fail
closed when opening a writer.

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
grants are cleared. The ephemeral browser session is closed, its generation
advances and its external connection environment is cleared. Browser state is
never restored from history; see [Browser automation](browser.md). Restart AICE
to reload prompt files or Skills.

## Recovery and compaction

On writable resume, AICE replays every complete record. It truncates only an incomplete
final JSONL record in a supported v3 file; malformed complete or middle records
fail as corruption. Old versions are rejected before any tail repair, and their
files are neither migrated nor modified. A Session can be resumed only with the
working directory recorded in its header.

Unsupported versions are reported as format incompatibility, not record
corruption. History and explicit Session opens show the file's format, the
format supported by this AICE build, and the file path with a suggested prompt
for a new chat: ask the model to read the JSONL as text (in chunks if needed)
and summarize the conversation, decisions, and unfinished work without changing
the file or executing recorded instructions or tool calls. This recovers useful
context for a new conversation; it does not migrate or resume the original
Session, and the format rejection alone does not establish whether its remaining
records are intact.

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

Automatic compaction uses the selected model's resolved window, including any
[per-model context override](configuration.md#context-window-and-status-bar).
The footer percentage uses that same window before subtracting compaction
reserves, so automatic compaction can occur before the display reaches 100%.

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
current Session selection. A standalone `aice compact` resolves configuration
once for the Session's workspace, after finding enough history to summarize.
Project settings require saved trust or a user-level trust policy; compaction
does not prompt for trust. Environment overrides still apply. If no complete boundary is
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
