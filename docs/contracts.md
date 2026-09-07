# LLM, Agent, Concurrency, and TUI Contracts

## Lifecycle vocabulary

| Term | Meaning and owner |
| --- | --- |
| Process | One AICE invocation; `internal/app` prepares its workspace, startup prompt, skills, and dependencies |
| Session | One durable JSONL tree, potentially resumed by later processes; `/new` detaches it |
| Agent run | One `Loop.Run` call: the initial interaction plus queued follow-ups, with frozen dependencies |
| Interaction | Initial/follow-up user input, in-interaction steers, and model/tool rounds until settlement; its source messages are persisted individually |
| Model round | One assistant response and its paired tool results; `turn_start`/`turn_end` events refer to this level |
| Side thread | Ephemeral `/btw` context and answers, owned separately from main Session history |

Avoid using “run” to mean process lifetime or Session lifetime, especially in
permission messages. Guard grant scope is defined in
[Execution](execution-sessions.md#tool-execution-boundary).

## Messages and model boundary

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
  Product behavior of the gate, including Session-scoped grants,
  is in [Tool execution and
  Sessions](execution-sessions.md#tool-execution-boundary).
- Never execute an incomplete or invalid streamed tool call. If a response
  stops for length with tool calls, execute none of them and return paired
  error results so the model can retry safely.
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

- Propagate `context.Context` through model calls, Agent runs, tools, and
  persistence boundaries. Do not store it in structs or replace it mid-flow
  with `context.Background()`. Bounded durable cleanup is an explicit exception:
  per-message Session submission uses `context.WithoutCancel` plus a five-second
  timeout to preserve known results after request cancellation. Initial
  lazy Session creation currently uses a local background context because
  `Runner.NewRun` has no context parameter; do not copy that into model/tool I/O.
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
- The built-in Guard has mutable grants without locking. Sequential tool
  execution protects only a single caller; sharing that Guard across concurrent
  runs is unsupported until its ownership/synchronization is changed. Tool-free
  side threads do not share this execution path.
- Each run owns its event stream. The sender closes the channel; receivers do
  not. Blocking sends also select on `ctx.Done()`.
- `internal/interaction`, wired by `internal/app`, owns one bounded, ordered
  mailbox per active run. Enter submits a steer and Ctrl+Enter submits a
  follow-up. At a natural stop boundary the mailbox atomically seals if empty,
  or promotes remaining steers and returns the oldest input as the next
  follow-up. Accepted input must not disappear in a run-end race.
- Interactive Ask confirmation is frontend-neutral: `internal/app` sends
  `interaction.GuardRequest` (`Options`, `Highlight`) and the frontend
  replies once with `GuardReply` (`OptionID`, `Feedback`). Product option
  generation and grant scope are in [Tool execution and
  Sessions](execution-sessions.md#tool-execution-boundary).
- Pending TUI permission prompts own the screen. A Bubbles viewport wraps the
  complete command, path, reason, and option details without ellipses; long
  option labels are shown there under their option numbers, with matching
  numbered controls fixed below. PgUp/PgDn, Home/End, and the mouse wheel
  scroll review content; ↑/↓ markers indicate hidden content. Arrow keys
  select options independently. Resizing recalculates the review height
  after reserving controls; closing the prompt restores the conversation.
- The TUI keeps only presentation copies. Pending steers are transcript
  previews until the Agent accepts them; follow-ups remain composer chrome
  until the Agent starts their interaction. Agent input events, not TUI queue
  policy, move those copies into the transcript.
- The application publishes current context occupancy separately from cumulative
  Session usage. Main runs derive it from their frozen settings and a run-local
  projection of recorded messages, replaced on successful compaction; completed
  tool results contribute before the next model request. Non-delta frontend
  events carry immutable context snapshots. Startup and command completion
  rebuild from the active conversation. The TUI only formats used percent;
  it does not count tokens or own provider limits.
- Only Bubble Tea's update loop mutates UI state. The application bridge turns
  Agent events into frontend-neutral interaction events; the TUI does not
  depend on `internal/llm`.
- The welcome-screen update check runs as a context-bound Bubble Tea command
  after the first render. Its result returns through the update loop; it never
  writes around the renderer or blocks terminal startup.
- Dragging transcript text copies the selection on release using the terminal's
  clipboard support. A bordered confirmation bubble without an explicit
  background floats centered immediately above the composer for one second
  in both main and BTW views, independently of Agent activity, footer content,
  and composer layout. Repeated copies
  restart the confirmation lifetime; streaming events do not dismiss it.
- Session history, model context, and terminal viewport remain separate.
  Streaming deltas are coalesced before expensive Markdown rendering.
- Terminal cell updates remain owned by Bubble Tea and its Ultraviolet
  renderer. Changed lines containing wide characters are repainted from the
  line boundary so partial erases cannot split CJK glyphs during streaming.
  Keep the renderer's wide-line repaint support when changing dependencies;
  `TestTerminalRepaintsChangedWideText` exercises actual terminal output rather
  than only checking the text returned by `View`.

### Interactive authentication

Account login uses the existing cancellable slash-command lifetime. The app
owns OAuth orchestration and credential persistence; the provider owns the
protocol. `interaction.AuthInteraction` carries transient progress and manual
input between the command and TUI. The TUI owns menus, browser/device prompts,
and hidden authorization input; none of these secrets enter transcript entries,
Session history, prompt history, steering, or queued model input. Completion
and cancellation discard the transient authentication state.
