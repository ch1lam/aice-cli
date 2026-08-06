# LLM, Agent, Concurrency, and TUI Contracts

## LLM and Agent Contracts

- AICE owns the canonical `Message`, `AgentMessage`, `UserMessage`, `AssistantMessage`, `ToolResultMessage`, `ContentPart`, `ToolCall`, `ToolResult`, `Model`, `Usage`, `StopReason`, `AgentEvent`, and stream abstractions.
- Follow Pi's message semantics while expressing them idiomatically in Go:
  - `Message` is the closed set of standard LLM-understood messages: `UserMessage`, `AssistantMessage`, and `ToolResultMessage`.
  - `AgentMessage` is the complete transcript-level message abstraction. It includes standard `Message` values and may later include AICE- or extension-defined messages.
  - Keep the complete `AssistantMessage` in history and sessions, including API, provider, model, response identifiers, usage, stop reason, error information, content, and timestamp. Never reduce it to a lossy role/content envelope.
  - Transform `[]AgentMessage` into `[]Message` only at the LLM request boundary. Protocol adapters then translate `[]Message` into provider SDK or wire types.
  - Session storage is lossless source history. Model context is a derived view and may filter or transform messages without rewriting the stored transcript.
- Keep provider identity/configuration separate from API protocol adapters. Compatible providers should reuse the protocol layer instead of duplicating a full client.
- Normalize text, reasoning, tool-call deltas, usage, finish reasons, errors, and cancellation without losing information.
- The Agent Loop is responsible for model calls, validated tool execution, tool-result history, natural continuation, and terminal lifecycle events.
- Do not impose fixed `MaxTurns` or `MaxToolSteps` limits. Continue while a completed assistant turn requests tools and the paired tool results can be returned to the model. Stop when the model completes without tool calls, the caller cancels or reaches its deadline, context protection rejects the next request, or a provider, protocol, runtime, or event-sink failure terminates the run.
- Never execute an incomplete or invalid streamed tool call. If an assistant response stops for length while containing tool calls, execute none of them; emit paired error tool results so the model can issue complete calls on a later turn.
- Preserve partial assistant output on cancellation or provider failure when it is safe to do so. Provider and runtime failures should produce a terminal assistant result or an explicit durable operation error rather than silently disappearing from history.
- Execute tools sequentially by default. Parallel execution may be added later only for independent read-only tools with explicit ordering and cancellation tests.
- Use a faux provider and fake tools as the primary executable specification for event ordering and loop behavior. Unit tests must not require paid APIs or real credentials.
- A second independent protocol (OpenAI Chat Completions) validates that the `llm` abstraction is genuinely provider-neutral. Do not generalize for further hypothetical providers before a concrete need exists.

## Concurrency and TUI Boundaries

- Pass `context.Context` as the first parameter through every model request, agent run, and tool execution. Never store a context in a struct or replace a caller context with `context.Background()` mid-flow.
- Every goroutine must have a clear owner, cancellation path, and wait/exit path. Avoid fire-and-forget work.
- Each agent run owns its event stream. The sender closes its channel; receivers never close it. All blocking sends must also select on `ctx.Done()`.
- Keep queues and buffers bounded. Do not use global channels or a permanent cross-run event bus.
- The TUI model is presentation state, not session truth. Session history, model context, and terminal viewport are separate concepts.
- Only Bubble Tea's update loop mutates UI state. Coalesce streaming deltas before expensive Markdown rendering instead of reparsing the full message for every token.
- Do not use nonexistent convenience APIs such as `tea.ChannelCmd`; wrap one channel receive in a `tea.Cmd` and register the next receive from `Update`.
