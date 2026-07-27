# AICE Development Guide

## Project Direction

- AICE is a batteries-included coding agent implemented in pure Go and inspired by Pi's design philosophy.
- The current product evolution priority is Session management: an append-only JSONL tree with restart recovery, branch navigation, backtracking, and branch-local manual compaction.
- Ship useful defaults that satisfy most users without customization, but keep providers, tools, terminal UI, persistence, execution environments, and future extensions replaceable at explicit boundaries.
- Self-extensibility is a product requirement. Users should eventually be able to adapt AICE without forking or modifying its internals. Preserve the seams needed for that direction, but do not build a speculative plugin framework before the core contracts and sessions are stable.
- Build a single Go module and a single AICE binary. Built-in tools may spawn explicit host executables such as Bash and ripgrep; do not introduce a daemon or additional AICE service. Keep the architecture small until real requirements justify more layers.
- The removed TypeScript implementation is not a compatibility target. Do not restore Node.js, npm, Vercel AI SDK, Ink, oclif, or the old session/config formats.
- Pi is the semantic and philosophical reference, not a directory template. Preserve its small coding-harness philosophy, provider-neutral messages, Agent Loop, tool and event lifecycles, session truth, and extensibility without copying its TypeScript monorepo structure.
- Match Pi domain names when the semantics are the same and the names remain idiomatic Go. Do not rename established concepts merely to make AICE look different.
- `dimetron/pi-go` may be used as an implementation reference only for features that overlap with original Pi, such as Cobra, Bubble Tea, tools, sessions, and release engineering. Do not adopt its Google ADK core or its additional platform features.
- Do not add ADK, Eino, LangChainGo, Genkit, a Node bridge, multi-agent orchestration, LSP, RPC/ACP, memory services, or other platform features before a concrete product requirement expands the scope.
- Code copied from Pi or another Go port must record its repository and commit and preserve all required MIT copyright and license notices. AICE remains Apache-2.0; do not copy third-party code without reviewing license compatibility.

## Target Technology Stack

- Language: Go, using the version declared by `go.mod`.
- CLI: Cobra. The command layer owns flags, arguments, help, and exit behavior. The application composition root owns dependency assembly and process lifecycle.
- TUI: Bubble Tea v2, Bubbles, Lip Gloss, and Glamour. Bubble Tea owns terminal rendering; channels only bridge asynchronous agent events into `tea.Msg` values.
- LLM layer: AICE-owned provider-neutral types and streaming contracts, backed by official provider SDKs or the Go standard library.
- First provider: DeepSeek through an Anthropic Messages protocol adapter using the official `anthropic-sdk-go` SDK with a configurable base URL.
- Persistence: append-only JSONL sessions. Use standard library packages before adding storage frameworks or databases.
- Execution: built-in tools use the host process environment by default. The built-in `grep` tool requires `ripgrep` (`rg`) on `PATH` and has no pure-Go fallback. Containers, sandboxes, or replaceable execution environments may provide stronger isolation.
- Configuration: standard library parsing, `AICE_*` environment variables, and the `.aice` directory. Keep secrets outside the repository.
- Logging and cancellation: `log/slog`, `context.Context`, and `signal.NotifyContext`.

Do not add an external dependency when the standard library is sufficient. Before adding an unlisted direct dependency, explain why it is needed, check maintenance and license status, and ask the user for approval. `ripgrep` is an approved runtime dependency for the built-in `grep` tool because its search performance and ignore semantics are part of the intended behavior; do not restore the removed pure-Go search implementation as a fallback.

## Intended Project Structure

```text
cmd/
└── aice/
    └── main.go                 # minimal process entry point

internal/
├── app/                        # composition root and lifecycle
├── cli/                        # Cobra commands and non-interactive output
├── tui/                        # Bubble Tea model, update, view, event bridge
├── agent/                      # bounded Agent Loop, lifecycle events, Tool contract
├── llm/                        # AICE messages, content, models, usage, stream contract
├── api/
│   └── anthropic/              # Anthropic protocol request/stream translation
├── provider/
│   └── deepseek/               # DeepSeek auth, models, defaults, compatibility
├── tool/                       # read, search, write, edit, bash implementations
├── session/                    # JSONL history, context views, compaction
└── config/                     # config and credential loading

testdata/                       # fixtures and golden inputs
```

Create packages only when implementation requires them; do not scaffold empty layers. Avoid vague catch-all packages such as `core`, `types`, `services`, `utils`, or `helpers`.

Dependency rules:

- `cmd/aice` stays minimal and delegates process assembly and execution to `internal/app`.
- `internal/app` is the composition root. It owns lifecycle and assembles CLI behavior, models, tools, sessions, execution environments, and the TUI.
- `internal/cli` owns only the command surface and invokes application capabilities through small consumer-defined interfaces.
- `agent` depends only on small AICE contracts, primarily `llm`; it must not import Cobra, Bubble Tea, provider SDKs, or concrete tools.
- `api/anthropic` translates between SDK/protocol data and `llm` types. Provider SDK types must not leak beyond the adapter.
- `provider/deepseek` supplies DeepSeek-specific configuration and compatibility behavior while reusing the Anthropic Messages protocol adapter.
- Concrete tools implement interfaces defined by their consumer. The Agent Loop must not construct tools itself.
- `tui` consumes agent events and converts them to `tea.Msg`; the agent must never emit or import Bubble Tea types.
- Built-in implementations use the same explicit contracts intended for future replacements and extensions. Do not give built-ins privileged access to core internals.
- Wire dependencies explicitly with constructors in the composition root. Do not use global registries, service locators, `init()` wiring, or a DI framework.

## LLM and Agent Contracts

- AICE owns the canonical `Message`, `AgentMessage`, `UserMessage`, `AssistantMessage`, `ToolResultMessage`, `ContentPart`, `ToolCall`, `ToolResult`, `Model`, `Usage`, `StopReason`, `AgentEvent`, and stream abstractions.
- Follow Pi's message semantics while expressing them idiomatically in Go:
  - `Message` is the closed set of standard LLM-understood messages: `UserMessage`, `AssistantMessage`, and `ToolResultMessage`.
  - `AgentMessage` is the complete transcript-level message abstraction. It includes standard `Message` values and may later include AICE- or extension-defined messages.
  - Keep the complete `AssistantMessage` in history and sessions, including API, provider, model, response identifiers, usage, stop reason, error information, content, and timestamp. Never reduce it to a lossy role/content envelope.
  - Transform `[]AgentMessage` into `[]Message` only at the LLM request boundary. Protocol adapters then translate `[]Message` into provider SDK or wire types.
  - Session storage is lossless source history. Model context is a derived view and may filter or transform messages without rewriting the stored transcript.
- Keep provider identity/configuration separate from API protocol adapters. Anthropic-compatible providers should reuse the protocol layer instead of duplicating a full client.
- Normalize text, reasoning, tool-call deltas, usage, finish reasons, errors, and cancellation without losing information.
- The Agent Loop is responsible for model calls, validated tool execution, tool-result history, continuation, maximum turn/tool-step limits, and terminal lifecycle events.
- Never execute an incomplete or invalid streamed tool call. If an assistant response stops for length while containing tool calls, execute none of them; emit paired error tool results so the model can issue complete calls on a later turn.
- Preserve partial assistant output on cancellation or provider failure when it is safe to do so. Provider and runtime failures should produce a terminal assistant result or an explicit durable operation error rather than silently disappearing from history.
- Execute tools sequentially by default. Parallel execution may be added later only for independent read-only tools with explicit ordering and cancellation tests.
- Use a faux provider and fake tools as the primary executable specification for event ordering and loop behavior. Unit tests must not require paid APIs or real credentials.
- A second independent protocol, such as OpenAI, is the eventual validation that the `llm` abstraction is genuinely provider-neutral. Do not generalize for hypothetical providers before that need exists.

## Concurrency and TUI Boundaries

- Pass `context.Context` as the first parameter through every model request, agent run, and tool execution. Never store a context in a struct or replace a caller context with `context.Background()` mid-flow.
- Every goroutine must have a clear owner, cancellation path, and wait/exit path. Avoid fire-and-forget work.
- Each agent run owns its event stream. The sender closes its channel; receivers never close it. All blocking sends must also select on `ctx.Done()`.
- Keep queues and buffers bounded. Do not use global channels or a permanent cross-run event bus.
- The TUI model is presentation state, not session truth. Session history, model context, and terminal viewport are separate concepts.
- Only Bubble Tea's update loop mutates UI state. Coalesce streaming deltas before expensive Markdown rendering instead of reparsing the full message for every token.
- Do not use nonexistent convenience APIs such as `tea.ChannelCmd`; wrap one channel receive in a `tea.Cmd` and register the next receive from `Update`.

## Tool Execution and Sandbox Boundaries

- AICE does not have an intrinsic permission or approval system. By default, built-in tools run with the filesystem, process, network, and credential permissions of the AICE process, matching Pi's ownership model.
- Do not add approval prompts or authorization policy to the Agent Loop. Isolation and policy belong to an explicitly selected container, sandbox, execution environment, or extension.
- Keep correctness and resource-safety checks even when no sandbox is active: validate arguments, reject malformed paths and inputs, propagate cancellation, bound time and output, preserve exit status, and pair every tool call with a result.
- Path handling must be deterministic and must honor the selected execution environment. A workspace helper is not a security sandbox and must not be described as one.
- Structured command execution should use `exec.CommandContext` with executable and arguments separated. The `grep` tool invokes `rg` this way and must place `--` before the model-supplied pattern and path. A Pi-compatible `bash` tool may intentionally invoke a shell; make that boundary explicit and apply the same cancellation, process-tree, environment, and output controls.
- The execution environment owns working-directory, environment-inheritance, filesystem, network, and executable policy. Do not hard-code those policies into the Agent Loop.
- Bound time, stdout, stderr, and total captured output. Preserve exit status and distinguish a command failure from an internal tool failure.
- Ensure cancellation terminates the complete spawned process tree and does not leave background processes behind.
- Redact secrets, credentials, and sensitive prompt content from logs and persisted diagnostic data.

## Sessions and Compaction

- Persist the complete original session as append-only JSONL. Do not overwrite source history during compaction.
- Persist complete turns and compaction checkpoints as tree nodes with stable IDs and parent IDs. Persist backtracking as append-only leaf moves; never delete the abandoned branch.
- Derive the active transcript from the root-to-leaf path. After moving the leaf to an older node, the next complete turn becomes a new child and therefore creates a branch.
- Keep session storage, the context sent to the model, and the TUI viewport as separate representations.
- Run compaction only at a complete turn boundary. Never split an assistant tool call from its tool result or mutate history concurrently with an active run.
- Compaction applies only to the active branch. Its checkpoint must retain a stable first-kept turn ID so recovery and later branching do not depend on global array indexes.
- A compacted context is a summary plus recent messages; it is a derived view, not a destructive rewrite.
- Start with usage accounting, context-limit protection, and manual compaction. Add automatic compaction only after the JSONL and recovery behavior are stable.

## Go Code Quality

- Read files in full before wide-ranging changes, before editing a file not already fully inspected, and whenever the user asks for an investigation or audit. Do not base broad conclusions on search snippets alone.
- Prefer clear, idiomatic Go over patterns copied from TypeScript. Keep functions focused, handle errors early, and avoid premature abstractions.
- Package names are short, lowercase, singular, and specific. Do not create `util` or `helper` packages.
- Define small interfaces where they are consumed; accept interfaces and return concrete types.
- Use manual constructor injection. Avoid mutable package globals and side-effectful `init()` functions.
- Wrap errors with context using `%w`; use errors for expected failures and reserve panics for broken invariants.
- Use concrete types or generics instead of `any` when the data shape is known. Keep raw JSON at protocol/tool boundaries.
- Propagate timeouts and cancellation to all external calls. Bound retries and check the context between attempts.
- Comments explain intent, invariants, protocol quirks, and security decisions, not obvious syntax.
- Never remove or downgrade code or capability to work around type errors caused by outdated dependencies. Upgrade the dependency and adapt the code to its supported API.
- Do not preserve backward compatibility unless the user explicitly requests it. This includes the abandoned TypeScript implementation and its session/config formats.

## Architecture Correction Order

Correct the current Go implementation with small, independently verified changes in this order unless the user changes priorities:

1. Correct `Message` and `AgentMessage` so history retains complete concrete messages and conversion happens only at the LLM boundary.
2. Prevent every tool call from a length-truncated assistant response from executing, with paired error results and focused tests.
3. Move permission and isolation ownership out of the built-in Agent/tool path and define the default host execution environment without weakening correctness or resource-safety checks.
4. Add append-only JSONL sessions, recovery, usage accounting, and manual compaction at complete-turn boundaries.
5. Expose the Pi-compatible mutating tools through the default host environment, then add an optional sandbox integration through the same replaceable execution boundary.
6. Add extension capabilities only after the core message, event, tool, and session contracts are stable enough to support them without privileged internal coupling.

The architecture-correction sequence above is complete. Continue Session work by extending the same JSONL tree and active-leaf model; do not add a second transcript store or a mutable database representation.

Keep structural changes, behavior changes, and imported upstream code in separate reviewable commits. Do not bulk-copy a Go port and then delete unwanted features in the same change.

## Verification Commands

- Documentation-only changes: run `git diff --check`.
- After Go code changes: format only modified Go files, then run `go test ./...` and `go vet ./...`.
- When `.golangci.yml` exists, also run `golangci-lint run ./...` and fix new findings rather than suppressing them.
- For concurrency, cancellation, channels, or shared-state changes, run `go test -race ./...`.
- If a test file changes, run the focused test while iterating, then the full unit suite before handoff.
- Tool and user-facing verification that exercises `grep` requires `rg` on `PATH`.
- Mark real-provider tests with an integration build tag and run them explicitly. Default tests must use faux providers and must not read provider credentials.
- Verify CLI/TUI work through the actual user-facing command. Do not treat package tests alone as proof that interactive behavior works.
- Do not invent build, release, changelog, or publishing commands before the repository defines them.

## Git and Collaboration

- Multiple sessions may share this worktree. Preserve unrelated staged, unstaged, and untracked changes.
- Modify and stage only explicit files owned by the current task. Never use `git add .`, `git add -A`, `git stash`, `git reset --hard`, `git checkout .`, or force push.
- Never commit unless the user asks. Before committing, inspect `git status` and the exact staged diff.
- When the user asks for incremental commits, make one independently verified commit after each completed small step.
- When asked to commit, follow the repository's existing short gitmoji/conventional subject style and keep each commit to one intent. Do not use Pi package scopes or release conventions.
- If a conflict touches a file not modified for the current task, stop and ask the user instead of resolving it speculatively.

## User Direction

The user's explicit request overrides this guide. If a request appears to conflict with an established invariant or architecture decision, explain the conflict and confirm the intended change. Once the user explicitly changes that decision, update this guide before implementing the new direction.
