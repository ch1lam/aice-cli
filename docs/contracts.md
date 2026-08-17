# LLM, Agent, Concurrency, and TUI Contracts

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
- History and Session turns retain complete assistant metadata: API, provider,
  requested and response models, response ID, usage, stop reason, errors,
  content, and timestamp.
- Conversion from `[]AgentMessage` to `[]Message` happens only at the LLM
  boundary. Protocol adapters then translate into SDK or wire types.
- Provider identity and model catalogs stay separate from protocol adapters;
  compatible providers reuse the protocol layer. Thinking translation switches
  on protocol-format metadata rather than provider or model IDs.

## Agent Loop

- The loop owns model calls, validated sequential tool execution, paired tool
  results, continuation, retries, and terminal Agent events.
- One run has two explicit levels. The inner loop continues through tool calls
  and steering. When it would otherwise stop naturally, the outer loop polls
  one follow-up; if present, it starts another interaction inside the same run.
  The application and frontend must not reproduce this stopping decision.
- There is no fixed `MaxTurns` or `MaxToolSteps`. A run ends only when the model
  completes naturally and no follow-up is waiting, or on cancellation/deadline,
  context protection, or an unrecoverable provider, protocol, runtime, or
  event-sink failure.
- Never execute an incomplete or invalid streamed tool call. If a response
  stops for length with tool calls, execute none of them and return paired
  error results so the model can retry safely.
- Preserve safe partial assistant output on cancellation or provider failure.
  Failures must become a terminal assistant result or an explicit durable
  operation error; they must not disappear from history.
- Poll steering input only after a complete assistant response and all tool
  calls declared by that response have matching results. Inject at most one
  user steer before the next model request, then offer the next steer at the
  following safe boundary.
- Poll follow-up input only at a natural stop boundary after steering has been
  checked. Emit `interaction_end` for the completed initial/follow-up
  interaction before polling again. Emit exactly one `agent_start` and one
  `agent_end` for the whole run, even when it contains multiple interactions.
- Reapply the compaction threshold before the first model request of every
  follow-up interaction. When the threshold is crossed at a complete
  interaction boundary, the application compacts the active Session and gives
  the loop the rebuilt provider-neutral history. Tool and steering
  continuations may settle the current interaction past that threshold, but a
  new interaction must not begin there.
- Tests use faux providers and fake tools. Default tests never require paid
  APIs or real credentials.

## Concurrency and TUI

- Propagate `context.Context` through model calls, Agent runs, tools, and
  persistence boundaries. Do not store it in structs or replace it mid-flow
  with `context.Background()`.
- Every goroutine has an owner, cancellation path, and wait/exit path. Queues
  and buffers stay bounded.
- Each `/btw` side thread owns a separate Runner, event stream, cancellation
  path, frozen parent-context snapshot, and bounded private history. An
  application-owned in-memory registry is authoritative for stable IDs,
  lifecycle, a five-thread limit, two concurrent side answers, a 20-minute
  follow-up window, and deletion after 120 minutes of inactivity. Running
  answers cross those thresholds and restart the idle clock when they
  terminate.
- The TUI owns one side-controller goroutine that starts independently
  cancellable per-thread runs and waits for all of them on shutdown. It keeps
  only presentation copies, routes batches by their source channel, and never
  applies one thread's events, draft, cancellation, or unread state to another.
  Side execution never mutates main history, Session records, usage, settings,
  or the main run mailbox.
- A main run snapshots its loop, model, options, and system prompt when it
  starts. Concurrent settings changes or side-thread creation must not swap
  those dependencies underneath the active run.
- Each run owns its event stream. The sender closes the channel; receivers do
  not. Blocking sends also select on `ctx.Done()`.
- `internal/interaction`, wired by `internal/app`, owns one bounded, ordered
  mailbox per active run. Enter submits a steer and Ctrl+Enter submits a
  follow-up. At a natural stop boundary the mailbox atomically seals if empty,
  or promotes remaining steers and returns the oldest input as the next
  follow-up. Accepted input must not disappear in a run-end race.
- The TUI keeps only presentation copies. Pending steers are transcript
  previews until the Agent accepts them; follow-ups remain composer chrome
  until the Agent starts their interaction. Agent input events, not TUI queue
  policy, move those copies into the transcript.
- Only Bubble Tea's update loop mutates UI state. The application bridge turns
  Agent events into frontend-neutral interaction events; the TUI does not
  depend on `internal/llm`.
- The welcome-screen update check runs as a context-bound Bubble Tea command
  after the first render. Its result returns through the update loop; it never
  writes around the renderer or blocks terminal startup.
- Session history, model context, and terminal viewport remain separate.
  Streaming deltas are coalesced before expensive Markdown rendering.
