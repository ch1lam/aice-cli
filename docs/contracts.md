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
  wire tokens, plus protocol-specific thinking-format metadata. Provider
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
  on protocol-format metadata rather than provider or model IDs. Anthropic
  Messages uses fixed-budget thinking by default; models declaring `adaptive`
  send adaptive thinking without `budget_tokens`. Effort support and whether
  thinking can be disabled remain catalog facts.

### Tool truncation metadata

[ToolTruncation](../internal/llm/truncation.go) is optional value-only source
metadata on tool results. Read records the limiting reason, returned source
lines/bytes (excluding notices), known/unknown totals and a 1-based continuation
offset. It never scans extra data solely to populate metadata; an oversized first
line leaves the offset unchanged. Grep adds match-limit and long-line flags,
counts formatted output, and has no read offset or source-total claim.

The Loop retains metadata through recording and events. App/TUI display it
without parsing prose or inferring data for old records. Protocol adapters and
print NDJSON project content only. See [read/grep behavior](execution-sessions.md#engine-only-configuration).

### Web evidence metadata

Tool results optionally retain an [evidence.Bundle](../internal/evidence/types.go)
from `web_search`/`web_fetch`, bounded to 64 KiB with referential integrity,
UTF-8 and closed-enumeration validation. IDs derive from normalized URLs.
Constructors and message clones copy it; old records remain nil.

[Web rendering](web.md#results-and-evidence) produces deterministic model content.
Operational timing/cost evidence does not participate in repetition detection.
App/TUI show source titles and URLs from metadata without opening them; adapters
and print output send content only.

### Structured tool outcomes

`llm.ToolResult` and `ToolResultMessage` retain optional `details` metadata
([types](../internal/llm/tool_output.go)): an explicit execution state, an optional
application-bound service/tool identity, structured JSON, and a notice of data
that could not be saved. States distinguish `not_dispatched`, `returned`
(including tool-reported errors) and `unknown`. An absent field means legacy
metadata is unavailable; it does not imply a known successful outcome.
Binding fingerprints are source metadata, not live handles or permission grants.
An optional binding `operation` distinguishes `resources/read` from ordinary
server tools (legacy absent operation). Resource readers carry immutable
service-bound definitions through the same complete-pair selection, Guard,
recording and request-budget boundaries. A same-name remote tool cannot alias
the resource operation's authorization.


Structured JSON is retained without decoding numbers into floating point and
is bounded to 1 MiB of valid UTF-8 JSON. A loss notice describes missing source
data, not model-only clipping, and must not promise recovery of unsaved bytes.
Constructors, nested result clones and Session snapshots copy mutable details
and image payloads. No protocol SDK values or new message roles are introduced.
This is an additive v3 field; existing records without it remain readable.

Serialization preserves the structured source's whitespace, JSON escapes,
number spelling and duplicate members. The existing `structured_content` JSON
value remains available to older readers. Only when normal JSON encoding would
change the source bytes, details also carry `structured_content_raw`, a JSON
string in that same record. New readers require both representations to agree
after lexical JSON normalization, then restore the exact bounded source; they
reject mismatches, malformed companions and oversized source. No float decoding
or object-map reordering is used for that comparison. The compatibility value
can expand through HTML escaping; its bound is six times the 1 MiB source bound.
Records without the companion retain their stored spelling; previously changed
whitespace or escapes cannot be reconstructed. Older readers can read the JSON
value but cannot preserve the new companion when rewriting such a record.

Adapters preserve content order and append structured JSON and uncertainty/loss
notices to the model projection. Anthropic and Responses retain ordered blocks
inside the tool result. Chat Completions cannot carry images in a tool message:
it emits a paired tool placeholder and, after the complete result group, a labeled
user projection of the entire multimodal result in source order. Text is not
detached from its neighboring images. Binding fingerprints and original image
bytes are not sent to providers. Context estimates include JSON and notices.

The Loop preserves partial content with explicit outcome metadata even when
`Execute` also returns an error. Invalid explicit outcomes become paired unknown
results with a loss notice; they cannot be misrepresented as an unexecuted action.
Legacy tool errors retain their existing text-only behavior. Repetition detection
includes explicit outcome details, so changed structured results count as
observable progress. The storage companion does not participate in that
comparison; retained whitespace/HTML spelling alone does not introduce progress.
Neither result conversion nor Session recovery reruns tools.

The application opts main runs into `RunInput.ResultViewTokens` and provides
`tool_result_read` through a run-owned reader capability. The Loop applies the
budget only to projected model messages after recording full source results;
its history, recorder, returned rounds and display events remain complete.
Zero preserves the existing full-view embedding contract. A changed projection
uses current request estimates rather than stale provider usage. Readback and
view bounds are specified in [MCP result readback](mcp.md#model-views-and-result-readback).

Tools may implement `BoundTool` to supply value-only provenance. Guard refusal,
version invalidation, pre-execution cancellation and synthetic unexecuted results
then retain `not_dispatched` plus that binding. The Loop never derives an identity
from a model-supplied tool name. Once execution starts, the tool's explicit
outcome remains authoritative; a legacy error does not become `not_dispatched`.

### Completed mutation diff metadata

[ToolDiff](../internal/llm/diff.go) records completed `write`/`edit` outcomes.
Tools compare original and committed bytes after atomic success; new files compare
against empty content. Missing/oversized/binary original content or binary output
can omit the diff without refusing a permitted write. Failures never produce a
successful diff, and old Sessions never reconstruct one from arguments/files.

Diffs use three context lines and missing-final-newline markers. Alignment is
bounded to one million LCS cells after common prefix/suffix removal, then uses
an exact but potentially non-minimal replacement. Inputs with 100,000 or more
newlines omit the diff. Stored output is bounded to 64 KiB / 2000 lines; known
added/removed counts cover alignment before clipping. These display bounds never
alter mutations. The implementation owns exact mechanics in
[edit_diff.go](../internal/tool/edit_diff.go).

App projects successful outcomes into `interaction.DiffDisplay`. Execution errors
or `IsError` suppress the successful diff and mark failure. TUI renders supplied
hunks/counts, escapes controls, and never rereads files. Adapters and print output
retain the short content summary; Session keeps the additive metadata.

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
- Runs have no model-round or tool-step ceiling by default. A run ends when the model
  completes naturally and no follow-up is waiting, or on cancellation/deadline,
  context protection, configured turn/resource limits, repeated-tool detection,
  or an unrecoverable provider, protocol, runtime, or event-sink failure.
- `RunLimits` are immutable configuration with per-run counters. Steering,
  follow-ups and automatic compaction share token/time budgets; compaction has
  its own turn counter. Resource/turn/repetition stops are non-retryable, retain
  actual outcomes and pair skipped calls. Exact accounting, overshoot and
  continuation behavior belong to [run limits](execution-sessions.md#run-resource-limits).
- Default model retries allow three retries after the first attempt, with
  2/4/8-second backoff. Longer provider retry hints are honored up to one minute;
  larger hints stop retries. Cancellation, protocol/context errors, event-sink
  failures and configured stop reasons are not retried. Tool execution is never
  retried by this policy. [retry.go](../internal/agent/retry.go) owns classification.
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
  The app uses it to reject write targets and bound MCP permissions changed
  during approval waits. A `GuardApproval` may carry gate-owned callbacks for
  explicit tool/service Session grants. The application invokes only the callback
  corresponding to an offered user choice; the Loop, allow-once and yolo never
  invoke them. Callbacks recheck identity/scope, have no tool effects, and remain
  transient rather than entering messages, Session records or frontend state.
  During `Execute`, the Loop also exposes that call-local revalidation through
  `CheckToolDispatch(ctx)`. A queued transport adapter calls it after acquiring
  its operation slot, before sending. It is a bounded local check, does not
  reenter the transport or hold a Guard lock across I/O, and cannot authorize or
  replay work. It catches permission changes while waiting after initial approval.
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

### Run-local tool selection

`RunInput.Catalog` is an optional consumer-owned catalog capability. The app
supplies immutable `CatalogTool` versions and stable ID/revision references;
the Loop owns the selected IDs, frozen per-request definitions and dispatch map.
The shared `Loop.tools` remains unchanged. A catalog requires a Guard even when
the initial builtin set is empty; every actual execution still crosses Guard.

A trusted tool may implement `ToolSelector`. After its Guard approval, the Loop
calls that capability instead of `Execute` and receives a typed proposal alongside
the ordinary result. At most five distinct candidates may be proposed. Only a
valid, non-error result whose recorder succeeds can add a pending proposal.
Selection is applied at preparation of the next complete model round. The same
assistant response cannot call tools discovered by an earlier call in its batch.
Tool-result prose and resumed Session messages never restore callable tools.

At the boundary, the catalog resolves selected stable IDs to current immutable
versions, dropping unavailable optional entries. The dispatch path checks the
request's exact revision before Guard and again after approval/revalidation;
invalidated or revoked versions receive a paired error without dispatch. Catalog
implementations must keep their version invalidation and final execution checks
consistent. Notifications cannot mutate the request's definitions. Model retries
reuse the existing tool snapshot, and catalog preparation errors are not retried
as model transport failures.

Dynamic definitions have a token estimate budget of 5% of the model context
window, capped at 8192 tokens; unknown windows use 4096. Most recently selected
tools take precedence, evicting complete older definitions with a model-visible
notice. Oversized single candidates are refused. App-supplied `PinnedTools`
cannot be evicted: missing pins or excessive pinned schemas fail preparation
with an explicit error. No schema is truncated. These defaults still require
task evaluation. Builtins remain under the ordinary whole-request budget.
Catalog runs reestimate context including current schemas instead of relying on
usage measured before tool loading. Selection is transient and isolated between
concurrent Runs, model changes, and Session resumes; it grants no permissions.

`RunInput.ObserveTools` optionally receives an owned ID/revision snapshot just
before each model request attempt, after preparation and budget checks. It
includes only catalog definitions in that request, including pins and excluding
evictions. A failed result recording or request preparation cannot publish a new
selection. Retries report the same frozen definitions. This synchronous observer
must return promptly and has no return value or authority; modifying its copy
cannot alter the Loop's selected versions. The caller clears its display state
when the Run ends. This is not a transcript or public NDJSON event.

The application supplies a fresh catalog for each MCP-enabled main run and
reuses only its authorized transport connections across runs. Configuration,
connection approval, discovery and lifecycle remain application responsibilities;
see [MCP](mcp.md#run-catalog-and-tool-search).

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

Computer Use tool events carry an optional immutable `DesktopDisplay` with an
application name and phase. The app's serial run event bridge derives this from
bounded original results and keeps a bounded display-only reference/name cache;
it performs no native I/O and creates no executable capability. The TUI owns one
transient activity row and existing tool folds. History derives the same facts
from Session records without replaying lifecycle events. Request, response,
unknown effect and setup-needed labels remain distinct; see
[Computer Use presentation](desktop.md#run-reference-and-result-contracts).

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

User controls belong to [Configuration](configuration.md#interactive-input-delivery).
The runtime boundary preserves these ownership rules:

- Propagate cancellation through preparation, model calls, tools, approvals and
  persistence. Every goroutine has an owner, cancellation and wait/exit path;
  queues are bounded and event senders close their own channels. Known Session
  results use explicit cancellation-independent cleanup bounded to five seconds.
- Each main run freezes loop, model, options, prompt, tools and connections;
  summaries use that same snapshot. A synchronized Guard permits concurrent
  input preparation and tool execution without holding its lock during approval
  waits or I/O. Reset requires active work to stop.
- App/interaction own the ordered bounded mailbox. The Loop accepts steers only
  after complete tool pairs and follow-ups only at natural settlement. The
  mailbox atomically seals when empty or promotes remaining steers; run-end races
  cannot drop accepted input. TUI pending messages are presentation copies.
- App owns each BTW thread's frozen context and private history. TUI controllers
  route events by source and cancel/join all thread runs on shutdown. Side
  execution never mutates main history, usage, settings or its mailbox.
- `GuardRequest`/`GuardReply` and `QuestionPrompt`/`QuestionReply` are separate
  cancellable exchanges. Replies are bound to the current request and validated;
  cancellation wins racing submissions. Q&A publishes only a complete explicit
  reply, never focus, drafts or recommendations. Permission UI may preempt it
  while retaining draft state. Neither exchange uses the steering mailbox.
- Only Bubble Tea Update mutates UI state. App translates Agent events into
  interaction types; TUI does not import LLM/tool/Session implementation types.
  Context occupancy arrives as immutable application snapshots, separately
  from cumulative Session usage.
- [input_context.go](../internal/tui/input_context.go) projects the active input
  domain from existing component state. [input_bindings.go](../internal/tui/input_bindings.go)
  gives dispatch and help the same actions, modifiers, availability and aliases.
  Dialog input is exclusive; disabled reserved keys cannot fall through. Async
  editor replies carry input identity/generation and are rejected after domain,
  focus or draft changes. Pending completion keys cannot accidentally submit.
- Layout is measured during Update, not mutated in View. Paint, native caret
  and hit testing use the same cell geometry. Press/release validates identity,
  content and geometry; reflow, focus changes and stale targets cancel clicks.
  Drag selection retains a frozen view while new content arrives. Code copy
  uses original source mappings, preserving tabs/line endings independently of
  wrapping, highlighting or clipping.
- Streaming deltas batch for up to 16 ms or 64 events; lifecycle events flush
  immediately. Folds, previews and caches are TUI state. Collapsed content is not
  rendered; streaming thinking/arguments have bounded display tails/previews.
  These bounds never truncate source history or model requests.
- Main/BTW viewports anchor to an item and local row, lazily rendering reached
  items. Width/source changes invalidate text and geometry together; ordinary
  scrolling avoids formatting hidden history or rebuilding the composer.
  Completed large answers use grouped Markdown layout; a single large paragraph
  or list item can still require full group formatting. Literal code and its
  copy targets survive folding and lazy layout. See
  [markdown_history.go](../internal/tui/markdown_history.go) and
  [code_block.go](../internal/tui/code_block.go).
- Session restoration transfers an immutable original-history display snapshot
  once, without replaying execution events or counting usage again. Read-only
  history owns a separate presentation. Catalog reads, previews and title saves
  reject stale generations and are cancelled/joined on exit; only explicit
  restore changes the active Store. Saved renames survive UI cancellation.
- Bubble Tea/Ultraviolet owns terminal output. Preserve wide-character line
  repaint support on upgrades; `TestTerminalRepaintsChangedWideText` exercises
  actual terminal output, not just `View` strings.

The frontend owns presentation caches only: Session JSONL, model context and
terminal viewport remain separate representations with different lifetimes.

### Settings and Usage capabilities

App implements `SettingsReader`, `SettingsStatusReader`, `SettingsWriter`,
`SettingsActionRunner` and `UsageReader`. Snapshots carry copied public values,
not credentials or writable stores. Config owns types/defaults/frozen sources;
app owns choices, timing, validation and resource publication; TUI owns editors,
drafts and modal navigation. Boolean enable actions can invoke domain setup
without adding another writer or service.

[settings_lifecycle.go](../internal/app/settings_lifecycle.go) reserves settings,
preparation and active responses. Lock order is lifecycle before state/history/
side locks. Lifecycle/state locks never span file-lock waits, authorization or
model calls; history synchronization separately serializes Store operations.
Prepared main/BTW runs check resource revision before accepting input. Active
runs keep frozen dependencies, including follow-ups.

Draft revision and resource revision track different effects:

| Effect | Draft revision | Resource revision |
| --- | --- | --- |
| Published shared preference/runtime | Advance | Advance |
| Saved Trust/restart-only preference | Advance | Unchanged |
| API-key-only or Web-credential-only commit | Advance | Unchanged: existing clients retain old resources |
| Changed subscription OAuth credential, even if selection later fails | Advance | Advance: providers reread credentials |
| Browser change or modifying helper with uncertain effects | Advance | Advance |
| Validation failure/cancel before effects, unchanged operation | Unchanged | Unchanged |

Resource changes reject held prepared runs and make existing BTW snapshots
read-only. Operations prepare before writing and publish after atomic replacement;
cleanup warnings preserve commit facts. Preferences and credentials are separate
commits. Domain guides own [browser](browser.md), [Web](web.md) and [MCP](mcp.md)
partial-failure rules. MCP management must publish saved revocation even if later
preparation fails; new runs stop rather than reuse stale authority. Live MCP deny
may revoke/cancel one service during a run without replacing another owner.

Settings reads do not probe the desktop: a separate bounded status read is checked
against both revision and panel generation. Closing reads cancels them; closing a
submitted write cannot undo a commit. Domain prompts use transient modal input,
never the conversation composer. Query/action owners cancel and join at shutdown.
The panel's Stop action uses ordinary run cancellation and waits for completion.

Usage reads derive from copied source records without consuming restored display
state or creating empty Sessions. Read on open, lifecycle completion or manual
refresh, never on each token delta. Missing prices/usage remain unknown.

### Interactive authentication

Login menus supply the selected provider and credential action. Custom login
collects endpoint, API key and model in separate hidden form steps, then sends
endpoint/model as dedicated `CommandRequest` fields and the key as `Secret`.
The application validates and persists these values; it does not parse endpoint
or model configuration from command arguments or the secret.

Account login uses the shared application operation under the cancellable
slash-command or Settings-action lifetime. The app owns OAuth orchestration and
credential persistence; the provider owns the protocol.
`interaction.AuthInteraction` carries transient progress and manual input between
the operation and TUI. The TUI owns menus, browser/device prompts,
and hidden authorization input; none of these secrets enter transcript entries,
Session history, prompt history, steering, or queued model input. Completion
and cancellation discard the transient authentication state. Menu selection,
manual input, and waiting resolve different bindings. Waiting accepts no editor
input; cancellation immediately disables submission and menu navigation, and
late progress cannot reactivate a cancelled login.

## Image content

Images carry model-facing view bytes and optional original bytes plus source
metadata. Originals are retained only when conversion, resizing or cropping
changes the view. These optional fields extend v3 messages without changing
roles or tool pairing; old v3 images remain readable. Clone both payloads across ownership
boundaries. Session JSONL is the durable owner; no sidecar image store is used.

Protocol adapters describe image IDs, sources, format conversion, first-frame
selection for GIF and coordinate mapping, and send only the view bytes.
Anthropic and Responses encode images inside tool results.
Chat Completions emits a tool placeholder for each multimodal result followed
by a labeled user message containing its ordered text and images after the
entire contiguous tool-result group. Text-only tool results stay in tool messages. This is a request
projection, never an additional user message in Session history.

File references in `interaction.RunInput.Files` and `Delivery.Files` are parsed
by the frontend before expanding literal paste placeholders. The application
replaces them with bounded text/image snapshots. The mailbox refuses unresolved
paths and owns only accepted content; it never reads files at dequeue time.
Concurrent file preparation and tool execution share a synchronized Guard.

`interaction.Command.SkillName` identifies a selectable skill, not an executable
slash command. The TUI binds it to a positional reference and sends exact names in
`RunInput.Skills` / `Delivery.Skills`. Application preflight validates the frozen
catalog, deduplicates references and loads complete instructions through Guard
before accepting the input. The mailbox refuses unresolved skill names. Skills
are ordinary accepted user-message content in the append-only Session; no marker
parsing or filesystem reads happen at dequeue. Selecting a normal command inside
a draft temporarily saves composer text, spans, paste/image data and cursor in
TUI state, restoring them after its command interaction ends.

`interaction.FileCompleter` exposes name-only search from app to TUI. The TUI
owns cursor/token state and stale-result rejection; app owns traversal budgets,
path resolution, ranking, and permission filtering. Search callbacks carry the
controller context and never block Update. Provider capability checks accept
image tool results for vision models through the same `SupportsImage` capability
used for user images; protocol adapters project the complete tool-call pairing.
