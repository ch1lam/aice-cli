# Tool Execution and Sessions

## Tool execution boundary

AICE has no intrinsic approval system. Built-in tools run with the filesystem,
process, network, environment, and credential permissions of the AICE process.
`--workspace` sets the default working directory; it is not an access boundary.

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
required. Project Trust does not change tool permissions; see [Project Trust
and prompts](project-trust.md).

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

`/btw` side threads are explicitly outside this persistence model. They use a
frozen copy of already accepted context plus their own bounded in-memory
history, run without tools, and never append `turn`, `compaction`, or `leaf`
records. They also do not contribute to main Session usage totals or compaction
input. Closing the panel does not cancel an active side answer; exiting AICE
discards the entire side thread.

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

Manual compaction summarizes older complete turns on the active branch and
retains roughly the newest 20,000 tokens. It appends a checkpoint and never
rewrites source turns or other branches:

```sh
aice compact --workspace . --session .aice/sessions/<id>.jsonl
```

`/compact` runs the same operation inside the TUI. Compaction uses the selected
provider/model, requires credentials and enough older history, and always uses
an AICE-owned prompt rather than project prompt files. Automatic compaction is
not implemented.
