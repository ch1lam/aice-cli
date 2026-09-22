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
count as progress. Result content, error status, diff and truncation metadata
participate in the comparison. Repeated denials are detected too.

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
`web_search` and `web_fetch` are known tools that pass through the same
execution gate without a web confirmation step: `web_search` allows when a
search service is bound (instance ID plus endpoint origin, set by the
application per run; without a binding it denies) and `web_fetch` allows
when the URL passes the shared URL-shape check (malformed URLs, userinfo,
zone-scoped IPv6 and non-default ports deny). Address validation, redirects
and body limits run inside the tool, outside the Guard lock. See [Web search
and fetch](web.md#permissions).
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

Tool descriptions and parameter schemas guide the model to use `write` for new
files or complete rewrites and `edit` for partial changes to existing files.
`write.content` is the complete final file content: omitted old content is not
preserved. These are model-facing selection rules, not an additional execution
gate. Tool-specific guidelines also appear in the default system prompt; the
descriptions and schemas remain available when a custom system prompt replaces it.

The `write` tool requires an explicit string `content`. Missing, `null`, or
non-string content returns a tool argument error before creating directories
or changing files. An explicit `content=""` remains valid and creates an empty
file or clears an existing file. Validation belongs to the tool; Guard still
checks permissions first. Once Guard allows the call, validation errors follow
the normal Loop path as paired results with `IsError=true`, allowing the model
to correct the arguments in its next request.

The `edit` tool validates all arguments before file access. `edits` must be a
non-empty array of objects, each with explicit string `oldText` and `newText`.
Missing, `null`, or non-string text is rejected; `oldText` must also be non-empty.
An explicit `newText=""` is a valid deletion. Entry errors identify the zero-based
`edits[index]` and the field when applicable. Any invalid entry leaves the whole
file unchanged. Unknown fields, stringified arrays, single-object edits, and
legacy top-level replacement fields are rejected.

`edit` matches every `oldText` against the same original file before writing;
later entries cannot depend on the results of earlier entries. The parameter
schema and default prompt guidelines explain this and require overlapping or
nested changes to be combined into one entry. Matching is exact apart from the
existing leading UTF-8 BOM preservation and CRLF/LF
normalization; Unicode and whitespace are not folded. Each match must be unique
(including self-overlapping occurrences), and replacement ranges must be disjoint.
Matching errors include the requested path and zero-based original `edits` indices.
Missing matches ask for a reread and whitespace/line-ending checks; repeated
matches report the count and ask for distinguishing context; overlapping edits
identify both input indices and ask for one combined edit. The default prompt
guidelines and missing/repeated-match errors instruct the model to correct the
edit and retry rather than use `write` merely to bypass a matching failure.
Any validation failure leaves the file unchanged. After all validation and
BOM/line-ending restoration,
`edit` compares the final bytes with the original. An identical result returns a
"no changes" tool error without preparing a write or reporting replacement success,
including when adjacent replacements cancel each other out. Mixed calls containing
changed and unchanged entries are accepted if every entry passes matching and
overlap validation and the final bytes differ. The existing success block count
includes all validated input entries.

`write` and `edit` serialize mutations within their shared Workspace. They hold
that lock until synchronous host file operations and temporary-file cleanup
finish, including after cancellation; cancellation does not leave background
writes running. Before preparing a temporary file and immediately before Rename,
they check cancellation. Cancellation observed before commit preserves the original
target (or leaves a new target absent) and removes the temporary file. Parent
directories already created may remain. Cleanup errors accompany the original
error. Rename is the commit point: once it starts, its actual outcome wins over
later cancellation. A successful commit remains a successful tool result even
when the Agent run then stops as canceled; the normal bounded Session recorder
preserves that known result. This does not make checking cancellation and Rename
one atomic operation or provide crash recovery for an interrupted process.

The `read` tool returns text or image content through one bounded reader.
Text offsets are 1-indexed. An offset beyond the last content line is a tool
argument error, returned through the normal Go error path and converted by the
Loop into a paired result with `IsError=true`. The error identifies the path,
requested offset, and actual line count known when the streaming read reaches
EOF; it does not trigger an extra full-file scan. A terminating newline does
not create an extra content line. An empty file read with the default offset
or explicit offset 1 succeeds with empty text; larger offsets fail. Reading
the last line succeeds with or without a terminating newline, with no
continuation notice. Legal pages retain their existing output limits and
continuation hints.

Its `image_id` input accesses only images already recorded on the active Session
branch, including sources no longer in compacted context. It performs no host
file access and has no path-access grant; unknown IDs fail. `path` continues to
use normal file permissions. A new Session cannot access a previous Session's
images. Stateless print retains sources only for that invocation.

Text reads preserve complete lines within 2000 lines and 50 KiB, including the
continuation notice. A smaller explicit limit retains the existing bounded
remaining-line count; only reaching EOF during that count yields a known total.
Default and byte-limited pages do not scan the rest of the file for metadata.
The TUI reports the reason, returned source lines/bytes, known or unknown total,
and continuation offset. A first line that cannot fit instead calls for bash.
Images, directories, errors, and untruncated text carry no text truncation data.
Captured line content grows on demand within the existing byte bound; skipping
and counting lines do not allocate content buffers.

Structured subprocesses use executable/argument separation. The `grep` tool
invokes `rg` with `--` before model-controlled pattern/path values. The `bash`
tool intentionally crosses a shell boundary and applies the same timeout,
output, cancellation, and process-tree controls.

Grep context reads are limited to 10 MiB per file. If a file cannot be read,
exceeds that limit, or the matched line has disappeared or changed, grep retains
the matching text already returned by ripgrep and reports why context is unavailable.
The fallback uses the same line and output limits as ordinary matches.
Each grep call caches context reads, including failures, for up to 128 files.
Retained text, line descriptors, paths and error text share a 16 MiB admission
budget; fixed entry overhead is bounded by the file cap. Files that do not fit
are read without caching. The cache is discarded after the call and does not
promise a filesystem snapshot. The 10 MiB file cap still applies to uncached
reads; cache admission does not bound temporary decoding allocations.
Grep records match-limit, byte-limit and long-line truncation metadata alongside
its model-facing notices. The TUI shows these reasons beneath the completed tool
row, including after Session replay, with search/refinement guidance rather than
read offsets. See [tool truncation metadata](contracts.md#tool-truncation-metadata).

Bash captures combined stdout/stderr within a 50 KiB result limit. Oversized
output retains its beginning and most recent end with an `[output truncated]`
marker between them, followed by exit status or the timeout reason. This keeps
final diagnostics available to the next model request. Capture storage stays
bounded even for a single large write; rendered output is valid UTF-8. Caller
cancellation still stops the process tree and returns cancellation.

On Windows, native Bash runs under a Windows Job Object. The optional WSL
fallback uses a Linux process group and a stdin lifetime channel: cancellation
closes the channel so a Linux-side watcher kills that command group. A bounded
host cleanup handles an unresponsive launcher; it never shuts down the whole
distribution. The workspace is mapped with `wslpath` before commands run.
Shell discovery, provisioning and WSL availability checks are described in
[Installation](installation.md#runtime-helpers).

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

Tool-result `evidence` is an additive optional field inside v3 source messages
recorded by `web_search` and `web_fetch`: sources, evidence items and
operational diagnostics, bounded to 64 KiB. It persists in the same record,
survives reopening, branches and history browsing, and is displayed as a source
list beneath the tool output. Old records without it remain valid and acquire
no inferred sources; compaction never rewrites it. Model requests carry the tool
content only. See [Web search and fetch](web.md#results-and-evidence).

Tool-result `truncation` is an additive optional field inside v3 source messages.
It persists in the same JSONL record as the content and survives reopening,
branch context reconstruction, and ordinary message copies. Old v3 messages
without it remain valid and do not acquire inferred metadata. Compaction leaves
source records intact. Replay restores the typed result, which uses the same
application display projection as a live result; it does not reconstruct metadata
from continuation prose. Startup and interactive resume hydrate the TUI from
the original active branch, including recorded tool outputs and diffs.

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

The Store retains a per-node tool-pairing state during append and replay. It
validates each new message against its parent's state without walking the whole
branch; pending call identities are copied before consumption so earlier
prefixes and sibling branches remain independent. Records and cached state
become visible only after a successful sync. This cache is derived in memory
and adds no JSONL record or durable format change.

The application tracks the last published leaf through `Store.ContextSince`.
Ordinary appends copy only the newly completed message group; an incomplete
group stays private until all results arrive. Checkout outside that ancestry
or a new compaction returns a complete replacement using `BuildContext`.
Initial loading also reconstructs the active context. Publishing committed
history and removing the matching pending input happen under the same
conversation lock, keeping side snapshots consistent. `Store.Info` answers
metadata-only queries without copying the transcript.

Message copies use the typed clone helpers in `internal/llm`, sharing immutable
strings while copying content slices, image payloads, tool arguments and cost
metadata. The write boundary still normalizes each new record through its JSON
representation. Full snapshots and contexts remain available for navigation,
compaction and isolated model runs; routine appends do not rebuild them.

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

Use `/history` or Ctrl+R in the idle TUI to open the current-project history
picker. It discovers regular `.jsonl` files under `<workspace>/.aice/sessions/`,
validates their workspace, hides empty sessions, and sorts by last recorded
activity, grouped as Today, Yesterday and Earlier in the local calendar. Each
group has a separate bold, blue subtitle with a count, disclosure arrow and
separator line, with a blank line above it. List focus highlights the selected
heading in gold without adding a `›`; that marker is reserved for sessions. Up/Down
and the mouse wheel select group headings as well as sessions. Enter on a heading
folds or unfolds its sessions, leaving the heading selected. Fold state and selection survive
additional search batches; changing the search query expands matching groups.
Group rows cannot be resumed, renamed or opened as transcripts, and do not load
a session preview. The total counts sessions, including folded ones. Cold
scans visit recently modified files first and publish an initial batch before
continuing through older files. Rows already loaded stay selectable and resumable;
Escape cancels scanning. Later batches preserve the selected session by identity.
The picker fills the terminal with a small outer margin. Its title and `[ ✘ ]` close
button sit on the top border, using the same thin, muted border as the slash
command menu. List rows keep two cells of right padding and a fixed five-cell
relative-time column, separated from title and snippet text by at least three
blank cells. Very narrow panes omit the time column. Compact two-line rows
show the custom title, or the first user prompt when none is set, recent content
and a current-session marker where applicable. Activity ages use `min`, `h`, `d`,
`m` and `y` (months use 30 days; years use 365 days); activity under one minute
shows `now`. Date group labels share the detail line. Malformed or
unsupported files are shown as unavailable rather than repaired. Sessions outside
this directory
remain accessible through an explicit startup `--session` path.

F2 edits the selected session's title. Enter saves; an empty value restores the
automatic title. Escape leaves the editor without switching sessions or losing
the live draft. Saving counts as session activity, refreshes search and preview,
clears the old search query, and preserves selection by file identity. A save
already committed to disk is not undone by closing its editor. Renaming reuses
the current session's writer or temporarily locks the selected file. Another
writer causes an error; an incomplete final JSONL record must be repaired by an
explicit resume first. Renaming never repairs history or recovers pending tools.

Search matches titles, filename stems, and user/assistant prose across all
branches, case-insensitively. Cached titles filter immediately; cancellable
body searches start after a 180 ms debounce. Validated title hits are published
before body scanning and remain ahead of body-only matches; result arrivals keep
the selected session stable. An active-branch match is preferred when available;
otherwise the result explicitly says that resuming will use the active branch.
Tool payloads, reasoning, and image bytes are not search targets. The application caches derived prose in memory,
bounded to 256 sessions and a 32 MiB accounting budget covering retained prose,
identifiers, summaries and per-entry overhead; oversized sessions are read
without retention. File identity, size and modification time invalidate
entries, and discovery evicts deleted files. JSONL remains the only durable
source; a cold or changed file is replayed read-only, one session at a time.
Body matching scans the original prose with a query prepared once per search,
stops at the first match in each message, and reuses its rune offset for the
excerpt. It does not allocate a lowercase copy of each complete message or
retain a second search-text copy in the catalog.
Preview is hidden and does not load by default, leaving the full width for the
list. Right opens it on demand and focuses the preview. Preview shows the last
active-branch user request and assistant answer with activity time, or up to six
matching excerpts, each limited to 1,200 runes. Conversation excerpts reuse the
main transcript’s Markdown rules, colors and syntax-highlighted code panels.
Preview code panels omit the transcript’s code-copy controls. The rendered
preview is reused until its text or available width changes.
Selection moves immediately; previews wait for a 120 ms pause, cancel obsolete
work, and keep the previous preview visible with a loading notice. Loading does
not block selection, closing, or resuming. Other-branch matches are labeled;
selecting a result still resumes the saved active branch without checking out
the matching message.

F4 opens a read-only transcript at the matched message (or the latest conversation
when there is no body match). Other-branch inspection is labeled and never writes
a checkout record. End returns to the latest active-branch conversation. T opens
a numbered directory of user questions; arrows and Enter jump to that question.
Escape returns to the picker with the original live draft and conversation intact.
Enter from transcript reading resumes the saved active branch. Ctrl+T in the idle
main conversation opens its question directory with the same reading controls.

The picker opens with list focus. `/` focuses the search field from either pane
without inserting the shortcut character. The field has no prompt prefix and
uses `/ to Filter` as its placeholder; the footer labels the shortcut `/ search`. A slash typed while search already
has focus is ordinary query text. Up/Down from search or clicking a list row
returns focus to the list; clicking the search field also focuses it.

Up/Down or a mouse click select a session or group heading. Enter restores a
session or toggles a selected group. With preview open, Left focuses the list
and Right focuses the scrollable preview.
The vertical mouse wheel acts on the pane under the pointer without changing
keyboard or search focus. It moves one list item/group or three preview rows
per event. The divider, borders, outside area and horizontal wheel events do
nothing. At a list boundary, wheel input preserves any pending preview request.
With preview enabled, wide terminals show both panes; narrow terminals show one
at a time. The picker has only its outer border, with a single vertical separator
between panes. The selected list item or group heading is gold only while the
list has focus; it returns to its normal text color when focus leaves. The
preview activity-date line stays fixed above the scrolling conversation, turning
gold with preview focus and returning to white otherwise. Clicking a pane also
focuses it; focusing search removes both pane highlights. The activity header
has a `⧉` button immediately after the time for copying the displayed session
ID. Hover highlights the character and shows “Copy session ID”; a left press
and release inside copies the ID and briefly confirms it. The button follows
the displayed preview while
a newly selected session loads, and is absent for groups, errors or missing IDs.
Escape hides an open preview and restores the full-width list, regardless of
which pane has focus. With preview hidden, Escape closes the picker. The top-right
`[ ✘ ]` button closes the picker directly. Closing preserves the current draft and
transcript position.
Hover and press turn the button characters error-red without changing the
background; pressing also makes them bold. Press and release inside it to close.
Dragging away, typing, scrolling, resizing or losing terminal focus cancels a
pending button press; returning to the button does not re-arm it.
During restoration, it requests cancellation like Escape. `/history <id>` restores
an existing local file by filename stem; it never creates a missing session.

Switching requires the main response and every BTW response to be idle. The
application validates and prepares the target before replacing the current
session. A failed switch preserves the old session and draft. A successful
switch clears old BTW threads and temporary Guard grants, closes and rotates
the browser session, and keeps the current provider/model/thinking settings.
It does not roll back workspace files or restore browser state. The visible
conversation uses original source records with completed tool/reasoning details
folded and the viewport at the end; model context still uses compaction
checkpoints. Long completed answers parse once and format only reached Markdown
sections, preserving lists, quotes, tables, reference links and original code
copy targets. Long standalone lists without code panels load complete items in
small groups as they become visible, retaining numbering and nested content.
Mouse-wheel scrolling reuses the surrounding layout and composer state.
Long code blocks show short previews; click the code heading or
press C in the reader (Alt+O in the main conversation) to expand the first visible
expandable block. Click, single-line and drag copy retain the literal code source without line numbers. Searching hidden code
opens the matching block and locates the original line. A single large paragraph,
individual list item, or list combined with code or other constructs still
requires its group's full prose layout.
`/checkout` uses the same original-history display projection.

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
