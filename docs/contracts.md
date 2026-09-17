# LLM, Agent, Concurrency, and TUI Contracts

## Lifecycle vocabulary

| Term | Meaning and owner |
| --- | --- |
| Process | One AICE invocation; `internal/app` prepares its workspace, startup prompt, skills, and dependencies |
| Session | One durable JSONL tree, potentially resumed by later processes; `/new` detaches it |
| Browser session | Ephemeral process/generation state owned by `internal/browser`, wired by app; see [Browser automation](browser.md#ownership-and-lifetime) |
| Agent run | One `Loop.Run` call: the initial interaction plus queued follow-ups, with frozen dependencies |
| Interaction | Initial/follow-up user input, in-interaction steers, and model/tool rounds until settlement; its source messages are persisted individually |
| Model round | One assistant response and its paired tool results; `turn_start`/`turn_end` events refer to this level |
| Side thread | Ephemeral `/btw` context and answers, owned separately from main Session history |

Avoid using “run” to mean process lifetime or Session lifetime, especially in
permission messages. Guard grant scope is defined in
[Execution](execution-sessions.md#tool-execution-boundary).

## Messages and model boundary

Interactive input can carry inline PNG/JPEG images alongside text or alone.
The application validates the selected model's image capability before accepting
an initial input or queued delivery. Input validation bounds image count, bytes,
and decoded dimensions; the mailbox owns a copy of accepted image data.
Initial inputs, steering, and follow-ups become the same canonical user content
blocks and persist inline in Session JSONL. Side-thread attachment submissions
are rejected explicitly. Protocol adapters retain responsibility for wire encoding.

- AICE owns `Message`, `AgentMessage`, concrete user/assistant/tool-result
  messages, content parts, tool calls, usage, models, stop reasons, events, and
  stream abstractions.
- `Model` carries a tri-state map from canonical thinking inputs to provider
  wire tokens, plus any Chat Completions thinking-format metadata. Provider
  catalogs own those facts. Application code derives distinct effective menu
  choices from the map, collapsing inputs that share a canonical token;
  protocol adapters encode the mapped value and reject unsupported requests.
- `Message` is the closed set accepted by normal LLM requests.
  `AgentMessage` also includes AICE-derived transcript context such as a
  compaction summary.
- History and Session messages retain complete assistant metadata: API, provider,
  requested and response models, response ID, usage, stop reason, errors,
  content, and timestamp.
- Conversion from `[]AgentMessage` to `[]Message` happens only at the LLM
  boundary. Protocol adapters then translate into SDK or wire types.
- Protocol adapters validate common request invariants with `Request.Validate`
  before translation, including message validity and tool definitions. Conversion
  helpers encode validated data; protocol-specific restrictions stay in adapters.
- Provider identity and model catalogs stay separate from protocol adapters;
  compatible providers reuse the protocol layer. Thinking translation switches
  on protocol-format metadata rather than provider or model IDs.

### Tool truncation metadata

`llm.ToolResult` and `ToolResultMessage` retain optional, value-only
`truncation` metadata ([type definition](../internal/llm/truncation.go)).
The read tool owns its creation after final complete-line trimming, including
space reserved for the model-readable continuation notice. Reasons distinguish
requested pagination, the default line cap, the byte cap, and a line that cannot
fit. `output_lines` and `output_bytes` count only returned source content (line
terminators included), excluding the notice and its separator. `next_offset` is
the 1-based first unreturned line. An oversized line returns zero source lines
and bytes, leaves that offset unchanged, and retains the bash fallback notice.
`total_lines_known: false` explicitly means the total is unknown; `total_lines`
is meaningful only when known. Metadata never triggers additional scanning.

Grep uses the same optional metadata with additive `match_limit_reached` and
`lines_truncated` fields. Its reason is `byte_limit` when the output budget is
hit, otherwise `match_limit` or `long_lines`; the extra fields preserve concurrent
limits. Counts describe formatted grep output (paths, context and inline context
failure diagnostics included), before final truncation notices. Grep has no
continuation offset and does not claim a source total. The app supplies search
refinement, limit adjustment or read guidance instead of read pagination.
Old records remain readable, and absent new fields decode to zero values.

The Loop carries these values through its existing result message, recorder,
and tool-end event. The app projects structured counts and a reason label into
`interaction.ToolDisplay`; the TUI displays truncation beneath the completed
tool row without parsing content text. The value-only projection participates
in normal viewport cache invalidation. Absent metadata (including old results)
produces no inferred status. Provider adapters continue sending content only;
this field does not change the curated print NDJSON projection.

### Completed mutation diff metadata

`llm.ToolResult` and `ToolResultMessage` carry optional, value-only `diff`
metadata ([type definition](../internal/llm/diff.go)). Edit and write compare
their original read with the final written bytes only after atomic write success.
For new files, write compares against empty content. Write reads at most 4 MiB
of original content for display; an unreadable, oversized or binary original
(or binary replacement) omits the diff and marks it incomplete, without rejecting
the permitted write. Unchanged writes record known zero counts.
Neither tool derives the diff from requested replacement snippets. Model-facing
content remains the short outcome summary; provider adapters and print text /
NDJSON continue projecting content only. The Loop retains metadata through its
normal result-message, recorder, and tool-end event paths.

Session JSONL persists this additive field without a version change. Missing
fields in older Sessions mean no diff is available; replay must not reconstruct
one from tool arguments or current files. Immutable strings need no separate
mutable ownership or transcript store.

The tool emits unified hunks with three context lines and no file headers,
retaining exact line terminators and explicit missing-final-newline markers.
Unchanged gaps are omitted using hunk coordinates. Alignment is capped at one
million LCS cells after common prefix/suffix removal; larger changed spans use
an exact, potentially non-minimal replacement block. Inputs with 100,000 or more
newline characters omit the diff entirely. Output retains at most 64 KiB / 2000
lines, stopping before a source row that cannot fit; `truncated` explicitly marks
omitted output, including an oversized first row. These presentation limits
never reject or alter a mutation. Failures carry no successful diff.
Optional `added`, `removed`, and `stats_known` fields retain full alignment counts
before output limits apply. They are additive Session metadata; absent fields in
older records mean counts are unknown. The input-line cap also leaves counts
unknown. Counts describe the bounded alignment, which may be non-minimal.

The app projects successful edit and write results into `interaction.DiffDisplay`;
either an execution error or `IsError` suppresses the diff and marks the tool failed.
The TUI shows known counts in the completed tool heading and renders supplied
hunks in a borderless block with addition/deletion backgrounds and old/new line
numbers. Complete legacy hunks can provide counts; incomplete legacy hunks cannot.
It never reads files or rematches arguments. Collapsed tools
hide their bodies; expanding a tool exposes up to the stored 64 KiB / 2000 lines.
Long rows are clipped to terminal width with a notice. Stored omissions remain explicitly
incomplete after expansion. Control and Unicode format characters are escaped
(including CR, so CRLF changes remain visible), literal backslashes are doubled,
and invalid UTF-8 displays as replacement runes. Diff values participate in the
normal transcript cache key, including replayed result projections.

## Agent Loop

- The loop owns model calls, validated sequential tool execution, paired tool
  results, continuation, retries, and terminal Agent events. Each inner-loop
  model round is `agent.ModelRound` (injected user inputs, one assistant
  response, and that response's tool results). A Session `MessageEntry`
  persists one source message; the complete model round is the safe boundary
  for deriving context when the assistant declares tool calls.
- One run has two explicit levels. The inner loop continues through tool calls
  and steering. When it would otherwise stop naturally, the outer loop polls
  one follow-up; if present, it starts another interaction inside the same run.
  The application and frontend must not reproduce this stopping decision.
- There is no fixed `MaxTurns` or `MaxToolSteps`. A run ends only when the model
  completes naturally and no follow-up is waiting, or on cancellation/deadline,
  context protection, or an unrecoverable provider, protocol, runtime, or
  event-sink failure.
- Before each tool execution the loop consults the consumer-defined `Guard`
  interface (`internal/agent` defines it, `internal/guard` implements it,
  `internal/app` wires it). `NewLoop` requires a non-nil `Guard` when the
  tool set is non-empty. `deny` blocks with a paired error result. An `ask`
  contains a nonempty list of independent approval scopes after all applicable
  hard-deny checks. The loop passes each scope to `GuardAskHandler` and executes
  the tool only after every scope is allowed. Non-interactive asks fail closed;
  `allow` proceeds. The handler returns
  `GuardAskReply` with Decision `allow` or `deny` and optional `Feedback`.
  Deny feedback is appended to the paired error tool result. A nil handler
  fails closed, as do invalid results/replies and canceled approval waits.
  A Guard result may carry a call-local `Revalidate` check. The loop runs it
  after all approvals and before tool execution; an error yields a paired
  tool error without execution. It cannot grant authority or execute tools.
  The app uses it to reject write targets changed during approval waits.
  Product behavior of the gate, including Session-scoped grants,
  is in [Tool execution and
  Sessions](execution-sessions.md#tool-execution-boundary).
- Never execute an incomplete or invalid streamed tool call. If a response
  stops for length with tool calls, execute none of them and return paired
  error results so the model can retry safely.
- Tool execution errors (including invalid read offsets) are converted by the
  Loop into paired `ToolResultMessage` values with the call ID, tool name,
  error text, and `IsError=true`. These results follow the same recording,
  tool-end event, and next-model-request path as successful results. A tool
  argument error alone does not terminate the run; the model can correct it.
- Preserve safe partial assistant output on cancellation or provider failure.
  Failures must become a terminal assistant result or an explicit durable
  operation error; they must not disappear from history.
- `RunInput.MessageRecorder` is an optional synchronous boundary for completed
  source messages. The Loop retains each accepted message before calling it,
  then displays the message and allows dependent effects. Tool results are
  retained and recorded before tool-end display. The callback receives a deep
  copy and the original run context; a durable implementation owns any bounded
  cancellation-independent cleanup deadline. The first recording error stops
  later execution and recording, including final cleanup. Display errors alone
  still allow known results and failure cleanup to be recorded once. Supplied
  history is never recorded again. The application injects this callback for
  message-level Session persistence; event delivery and interaction settlement
  do not append a second copy.
- Poll steering input only after a complete assistant response and all tool
  calls declared by that response have matching results. Inject at most one
  user steer before the next model request, then offer the next steer at the
  following safe boundary.
- Poll follow-up input only at a natural stop boundary after steering has been
  checked. Emit `interaction_end` for the completed initial/follow-up
  interaction before polling again. A normally delivered run emits one
  `agent_start` and one `agent_end`, including runs with multiple interactions.
  Preflight failures can return before `agent_start`; an event-sink failure
  stops delivery and may prevent `agent_end`. Callers must handle the returned
  result/error and persistence separately from terminal event delivery.
- Check the compaction threshold before every model request, including tool
  and steering continuations within one interaction. At a complete paired
  boundary the application may replace the full current context, including
  accepted user messages, with a summary and retained suffix. Unanswered
  trailing user messages remain verbatim. The Loop never appends them a second
  time. Retries reuse prepared context; tools never trigger compaction midway
  through a group. At most one compaction is attempted before the request is
  fully checked again; failure or insufficient space stops execution. Product
  behavior of compaction is in
  [Recovery and compaction](execution-sessions.md#recovery-and-compaction).
- Context estimates reuse provider usage only when its requested provider/model
  identity matches the current request and it belongs to the current context
  after compaction. Otherwise they estimate the complete prompt, tools, and
  messages. Main requests and summary requests share the same reserve/safety
  budget calculation; this remains an estimate, not a provider tokenizer.
- Tests use faux providers and fake tools. Default tests never require paid
  APIs or real credentials.

## Internal events

Tool-end events include a frontend-only `ToolOutputDisplay`: the first 64 KiB
of recorded result text, cut at a UTF-8 boundary with explicit display truncation.
Non-text parts are labelled rather than copying image payloads. An empty result
is distinguished from unavailable output; execution errors supply text when no
result text exists. This projection never rereads workspace files or changes
Session content, provider input, or print output.

[agent.EventType](../internal/agent/contracts.go) defines the closed string set
for Agent lifecycle events. [interaction.EventKind](../internal/interaction/contracts.go)
defines the frontend's internal numeric enum. Use those declarations when
changing events; do not treat numeric enum positions as a public wire format.
`internal/app` translates between them. `interaction_end` marks interaction
settlement and `turn_end` marks a completed model round. Neither event owns
persistence; the synchronous message recorder does.

The public print format below is a separate, curated projection. Adding an
internal event does not automatically expose it in NDJSON or justify serializing
frontend state.

## Print NDJSON events

`aice --print --output-format json` writes a stable, additive NDJSON stream to
stdout. Each line is one JSON object selected by `type`; consumers must ignore
unknown fields so later releases can add data without changing existing
fields. The stream deliberately omits token-level `text_delta` events: complete
assistant text and thinking are emitted once at `message_end`.

| `type` | Fields |
| --- | --- |
| `agent_start` | `type` |
| `message_end` | `role`, `text`, `thinking`, `usage`, `stop_reason`, `model` |
| `tool_execution_start` | `tool_call_id`, `name`, `arguments` |
| `tool_execution_end` | `tool_call_id`, `name`, `is_error`, `result`, `duration_ms` |
| `retry_start` | `attempt`, `max_retries`, `delay_ms` |
| `retry_end` | `attempt`, `success`, optional `error` |
| `agent_end` | total `usage`, optional `error` |

Only assistant messages produce `message_end`; tool-result message lifecycle
events are represented by `tool_execution_end` and are not duplicated. Tool
result text is limited to 16 KiB per event, including the trailing
`...[truncated]` marker. Durations and delays are integer milliseconds. The
`agent_end.usage` value includes every assistant `message_end` observed in the
run plus automatic summary attempts that report usage. Summary calls do not
produce main-assistant events. Failed summaries and failed checkpoint writes
still contribute known usage to the current print total; prior Session usage
is not added again. The text printer reports the same accounting in its total
diagnostic. As with other events, cancellation or output failure may prevent
the final JSON event from being delivered.

## Concurrency and TUI

User-facing fold controls, panel appearance, and copy gestures are documented
in [Interactive input delivery](configuration.md#interactive-input-delivery)
and [Tool output and code panels](configuration.md#tool-output-and-code-panels).
The contracts below define state ownership, bounded rendering, and coordinate
consistency for those behaviors.

- Propagate `context.Context` through model calls, Agent runs, tools, and
  persistence boundaries. Do not store it in structs or replace it mid-flow
  with `context.Background()`. Bounded durable cleanup is an explicit exception:
  per-message Session submission uses `context.WithoutCancel` plus a five-second
  timeout to preserve known results after request cancellation.
  `Runner.NewRun` and `ActiveRun.Deliver` take caller contexts. The TUI
  publishes cancellation before preparation starts; delivery preparation runs
  in a command, never on the Update goroutine. The application resolves explicit
  file references through Guard and the shared reader before acceptance.
- Every goroutine has an owner, cancellation path, and wait/exit path. Queues
  and buffers stay bounded.
- Each `/btw` side thread owns a separate Runner, event stream, cancellation
  path, frozen parent-context snapshot, and bounded private history. An
  application-owned in-memory registry is authoritative. Limits, idle
  windows, and TUI controls are in [Configuration](configuration.md#btw).
- The TUI owns one side-controller goroutine that starts independently
  cancellable per-thread runs and waits for all of them on shutdown. It keeps
  only presentation copies, routes batches by their source channel, and never
  applies one thread's events, draft, cancellation, or unread state to another.
  Side execution never mutates main history, Session records, usage, settings,
  or the main run mailbox.
- A main run snapshots its loop, model, options, system prompt, and connection
  configuration when it starts. Automatic summaries use that same frozen
  configuration; they do not reload global settings midway through the run.
  Concurrent settings changes or side-thread creation must not swap those
  dependencies underneath the active run.
- The built-in Guard synchronizes checks and mutable grants. Input preparation
  can check files while the Agent runs tools; approval waits hold no Guard lock.
  Session reset still requires active work to stop. Tool-free side threads do
  not share this execution path.
- Each run owns its event stream. The sender closes the channel; receivers do
  not. Blocking sends also select on `ctx.Done()`.
- `internal/interaction`, wired by `internal/app`, owns one bounded, ordered
  mailbox per active run. Enter submits a steer and Ctrl+Enter submits a
  follow-up. At a natural stop boundary the mailbox atomically seals if empty,
  or promotes remaining steers and returns the oldest input as the next
  follow-up. Accepted input must not disappear in a run-end race.
- Interactive Ask confirmation is frontend-neutral: `internal/app` sends
  `interaction.GuardRequest` (`Options`, `Highlight`, `Done`) and the frontend
  replies once with `GuardReply` (`OptionID`, `Feedback`). `Done` removes expired
  prompts, including attachment preparation cancelled by a finished run. Product option
  generation and grant scope are in [Tool execution and
  Sessions](execution-sessions.md#tool-execution-boundary).
- Pending TUI permission prompts own the screen. A Bubbles viewport wraps the
  complete command, path, reason, and option details without ellipses; long
  option labels are shown there under their option numbers, with matching
  numbered controls fixed below. PgUp/PgDn, Home/End, and the mouse wheel
  scroll review content; ↑/↓ markers indicate hidden content. Arrow keys
  select options independently. Resizing recalculates the review height
  after reserving controls. While a prompt is visible, transcript state keeps
  accepting updates but its rendering is deferred; terminal frames contain
  only the prompt. Closing it renders the latest conversation at the current
  terminal size and restores the composer.
- The TUI keeps only presentation copies. Pending steers are transcript
  previews until the Agent accepts them; follow-ups remain composer chrome
  until the Agent starts their interaction. Agent input events, not TUI queue
  policy, move those copies into the transcript.
- The application publishes current context occupancy separately from cumulative
  Session usage. Main runs derive it from their frozen settings and a run-local
  projection of recorded messages, replaced on successful compaction; completed
  tool results contribute before the next model request. Non-delta frontend
  events carry immutable context snapshots. Startup and command completion
  rebuild from the active conversation. The TUI formats the header percentage and its hover/click token fraction;
  it does not count tokens or own provider limits.
- Only Bubble Tea's update loop mutates UI state. The application bridge turns
  Agent events into frontend-neutral interaction events; the TUI does not
  depend on `internal/llm`.
- The welcome-screen update check runs as a context-bound Bubble Tea command
  after the first render. Its result returns through the update loop; it never
  writes around the renderer or blocks terminal startup.
- Transcript folding uses all-motion mouse reporting. Rendering and hit testing
  share visible wrapped rows tagged with stable fold targets; never infer a
  click from the approximate selection Y offset. Hover is derived from the
  pointer and current layout without reformatting hidden history. A click
  commits on left release only when no drag occurred and its target still
  matches. Fold changes anchor the clicked item/row instead of following the
  bottom. Permission screens and side views cannot activate main fold targets.
- Dragging transcript text copies the selection on release using the terminal's
  clipboard support. A bordered confirmation bubble uses the screen background
  and floats centered immediately above the composer for one second
  in both main and BTW views, independently of Agent activity, footer content,
  and composer layout. Repeated copies
  restart the confirmation lifetime; streaming events do not dismiss it.
  The TUI resolves canvas colors before composing the bubble, preserving
  foregrounds, backgrounds and padding outside its bounds, including Markdown
  heading rows.
- Session history, model context, and terminal viewport remain separate.
  The TUI coalesces streaming deltas for up to 16 ms or 64 events before
  rendering; lifecycle updates flush the batch immediately. Main and side
  views use the same batching rule. Only the update loop owns assistant
  accumulation buffers and per-section caches, keyed by source and width.
  Process, contiguous-call-group, thinking and tool folds are independent TUI
  state; parent folds retain child choices. Manual choices override automatic
  process folding during streaming. Only visible branch resets clear them.
  Collapsed contents are not rendered. During streaming, thinking
  renders only a UTF-8-safe tail of at most 4 KiB plus an omission notice;
  full content remains in the presentation snapshot and becomes available
  on completion. These display limits never truncate Session or model context.
  Write previews project existing tool-call start/delta/end events through the
  application bridge. The TUI accumulates at most 64 KiB of raw arguments per
  call and parses only for presentation when visible, using the existing event
  batching and item cache. Complete calls replace the partial preview; execution
  start reconciles the same row by call ID. A preview never authorizes execution.
  Read selects syntax highlighting from the filename without interpreting source
  as Markdown. Expanded write previews bound source input to
  2000 lines / 64 KiB. Lines are clipped before syntax
  highlighting and terminal control characters are escaped. These limits affect
  neither tool arguments nor Session history.
  Assistant Markdown code (fenced or indented), tool text and write previews
  use the same `internal/tui/code_block.go` component. It retains literal source
  and line terminators separately from escaped/highlighted rows. Layout maps
  every wrapped row to its zero-based source line; padding and empty-output
  labels have no source line. A final newline does not add a phantom line.
  Callers own source limits and completeness; component line counts describe
  supplied source, never an inferred complete file. Result text wraps and write
  previews clip using the same layout boundary. Diff keeps its own hunk/line
  rendering and shares only the panel decoration.
  `markdown.go` parses the whole document with Goldmark and replaces code AST
  nodes with collision-checked render slots, since Glamour's nested buffers
  do not expose a custom code rendering hook. The shared component fills those
  slots after prose layout, retaining document-local block coordinates and
  source-row mappings in the assistant cache. Renderer failure falls back to
  literal escaped source, never internal markers or partial content. Width and
  source changes invalidate text and block geometry together. These data are
  presentation-only. During streaming, `assistantPresentation` owns a
  `markdownCache` for both main and BTW answers. It still parses the complete
  source, but reuses unchanged groups of top-level nodes at the same width.
  Checkpoints precede a source-positioned paragraph or heading after a
  line-ending block; lists and quotes are never split internally. Rendering
  keeps the original AST parents, siblings and boundary newlines. Changed
  reference definitions invalidate all groups so earlier links resolve correctly.
  Cached code layouts retain literal source; composition rebases their row
  coordinates without modifying cached placements. Completion uses a full render
  and releases the streaming groups. Whole-source parsing and composition remain
  linear in answer size; a growing single paragraph, list or code block still
  needs its group's full layout. This is not an incremental syntax highlighter.
  `transcriptContent` carries text and block placements through
  composition, indentation and the viewport's lazy cache. Hit testing uses the
  same visible rows as painting, including main and BTW answers, tool output and
  write previews. Copy buttons use original supplied source and validate both
  source and geometry at release; scrolling, reflow and replaced content cannot
  reuse a stale press. Drag selection retains its frozen display snapshot.
  Single-click line copying uses the same source-row mapping, removes only the
  final LF/CRLF, and preserves all other characters. Wrapped and clipped rows
  resolve to their original logical line. Hover repaints only visible target
  cells, preserves syntax foregrounds and cached geometry, and is suppressed
  during selection and modal input. Diff rows retain their separate rendering
  and selection semantics.
  Tool headings count supplied source lines in parentheses using the same bounded
  source selection as their bodies, without rendering collapsed panels. Panel
  status rows have no source-line mapping. Tool truncation and incomplete write
  previews mark the heading count partial, and completed previews refresh the label.
  Main and BTW transcripts use an item-anchored viewport: scrolling records a
  block and a row within it, without measuring all preceding history. Process
  headers, individual reasoning/answer blocks, tools and questions are separate
  items, including multiple model rounds within a single process group. Only
  reached items are formatted and wrapped; unchanged visible items reuse their
  cached rows. Fold headings follow the same rule: historical thinking labels,
  tool-group summaries and tool headers are formatted only when reached, and
  hover variants only when hovered. Heading versions include fold state, tool
  completion and preview revisions, and live activity; unchanged historical
  headings reuse both normal and hover rows. Width changes invalidate wrapping,
  height changes retain it.
  Refreshes rebuild lightweight item descriptions but never concatenate the
  full transcript. Full-content snapshots are explicit operations, not part of
  animation, scrolling, or streaming frames. Selection freezes visible rows and
  their local coordinate snapshot while new content continues arriving.
  Completed content remains available by scrolling; a first visit to a large
  individual block can still require formatting that whole block.
- Terminal cell updates remain owned by Bubble Tea and its Ultraviolet
  renderer. Changed lines containing wide characters are repainted from the
  line boundary so partial erases cannot split CJK glyphs during streaming.
  Keep the renderer's wide-line repaint support when changing dependencies;
  `TestTerminalRepaintsChangedWideText` exercises actual terminal output rather
  than only checking the text returned by `View`.

### Interactive authentication

Login menus supply the selected provider and credential action. Custom login
collects endpoint, API key and model in separate hidden form steps, then sends
endpoint/model as dedicated `CommandRequest` fields and the key as `Secret`.
The application validates and persists these values; it does not parse endpoint
or model configuration from command arguments or the secret.

Account login uses the existing cancellable slash-command lifetime. The app
owns OAuth orchestration and credential persistence; the provider owns the
protocol. `interaction.AuthInteraction` carries transient progress and manual
input between the command and TUI. The TUI owns menus, browser/device prompts,
and hidden authorization input; none of these secrets enter transcript entries,
Session history, prompt history, steering, or queued model input. Completion
and cancellation discard the transient authentication state.

## Image content

Images carry model-facing view bytes and optional original bytes plus source
metadata. Originals are retained only when conversion, resizing or cropping
changes the view. These optional fields extend v3 messages without changing
roles or tool pairing; old v3 images remain readable. Clone both payloads across ownership
boundaries. Session JSONL is the durable owner; no sidecar image store is used.

Protocol adapters describe image IDs, sources, format conversion, first-frame
selection for GIF and coordinate mapping, and send only the view bytes.
Anthropic and Responses encode images inside tool results.
Chat Completions emits text tool results followed by an image-bearing user
message after the entire contiguous tool-result group. This is a request
projection, never an additional user message in Session history.

File references in `interaction.RunInput.Files` and `Delivery.Files` are parsed
by the frontend before expanding literal paste placeholders. The application
replaces them with bounded text/image snapshots. The mailbox refuses unresolved
paths and owns only accepted content; it never reads files at dequeue time.
Concurrent file preparation and tool execution share a synchronized Guard.

`interaction.FileCompleter` exposes name-only search from app to TUI. The TUI
owns cursor/token state and stale-result rejection; app owns traversal budgets,
path resolution, ranking, and permission filtering. Search callbacks carry the
controller context and never block Update. Provider capability checks accept
image tool results for vision models through the same `SupportsImage` capability
used for user images; protocol adapters project the complete tool-call pairing.
