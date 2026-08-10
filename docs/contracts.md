# LLM, Agent, Concurrency, and TUI Contracts

## Messages and model boundary

- AICE owns `Message`, `AgentMessage`, concrete user/assistant/tool-result
  messages, content parts, tool calls, usage, models, stop reasons, events, and
  stream abstractions.
- `Message` is the closed set accepted by normal LLM requests.
  `AgentMessage` also includes AICE-derived transcript context such as a
  compaction summary.
- History and Session turns retain complete assistant metadata: API, provider,
  requested and response models, response ID, usage, stop reason, errors,
  content, and timestamp.
- Conversion from `[]AgentMessage` to `[]Message` happens only at the LLM
  boundary. Protocol adapters then translate into SDK or wire types.
- Provider identity and model catalogs stay separate from protocol adapters;
  compatible providers reuse the protocol layer.

## Agent Loop

- The loop owns model calls, validated sequential tool execution, paired tool
  results, continuation, retries, and terminal Agent events.
- There is no fixed `MaxTurns` or `MaxToolSteps`. A run ends on a normal model
  completion, cancellation/deadline, context protection, or an unrecoverable
  provider, protocol, runtime, or event-sink failure.
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
- Tests use faux providers and fake tools. Default tests never require paid
  APIs or real credentials.

## Concurrency and TUI

- Propagate `context.Context` through model calls, Agent runs, tools, and
  persistence boundaries. Do not store it in structs or replace it mid-flow
  with `context.Background()`.
- Every goroutine has an owner, cancellation path, and wait/exit path. Queues
  and buffers stay bounded.
- Each run owns its event stream. The sender closes the channel; receivers do
  not. Blocking sends also select on `ctx.Done()`.
- The TUI owns one bounded, ordered pending-input mailbox per active Agent
  chain. Enter adds a steer, Ctrl+Enter adds a queued run, and the terminal
  transition atomically seals an empty mailbox or promotes remaining steers
  before starting queued runs. Accepted input must not disappear in a run-end
  race. Pending steers are presentation-only transcript previews until the
  Agent accepts them; queued inputs remain composer chrome until their run
  starts.
- Only Bubble Tea's update loop mutates UI state. The bridge turns Agent events
  into TUI-owned display messages; the TUI does not depend on `internal/llm`.
- Session history, model context, and terminal viewport remain separate.
  Streaming deltas are coalesced before expensive Markdown rendering.
