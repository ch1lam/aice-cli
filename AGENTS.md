# AICE Development Guide

## Project Direction

- AICE is being rewritten as a pure Go coding agent inspired by Pi's design philosophy.
- Build a single Go module, a single binary, and a single local process. Keep the architecture small until real requirements justify more layers.
- The removed TypeScript implementation is not a compatibility target. Do not restore Node.js, npm, Vercel AI SDK, Ink, oclif, or the old session/config formats.
- Original Pi is the behavior reference, not a directory template. Preserve its small coding-harness philosophy, provider-neutral agent semantics, tool loop, and event model without copying its TypeScript monorepo structure.
- `dimetron/pi-go` may be used as an implementation reference only for features that overlap with original Pi, such as Cobra, Bubble Tea, tools, sessions, and release engineering. Do not adopt its Google ADK core or its additional platform features.
- Do not add ADK, Eino, LangChainGo, Genkit, a Node bridge, multi-agent orchestration, LSP, RPC/ACP, memory services, or plugin frameworks unless the user explicitly expands the product scope.
- Code copied from Pi or another Go port must record its repository and commit and preserve all required MIT copyright and license notices. AICE remains Apache-2.0; do not copy third-party code without reviewing license compatibility.

## Target Technology Stack

- Language: Go, using the version declared by `go.mod`.
- CLI: Cobra. The command layer owns flags, arguments, help, exit behavior, and dependency assembly.
- TUI: Bubble Tea v2, Bubbles, Lip Gloss, and Glamour. Bubble Tea owns terminal rendering; channels only bridge asynchronous agent events into `tea.Msg` values.
- LLM layer: AICE-owned provider-neutral types and streaming contracts, backed by official provider SDKs or the Go standard library.
- First provider: DeepSeek through an OpenAI-compatible protocol adapter using the official `openai-go` SDK with a configurable base URL.
- Persistence: append-only JSONL sessions. Use standard library packages before adding storage frameworks or databases.
- Configuration: standard library parsing, `AICE_*` environment variables, and the `.aice` directory. Keep secrets outside the repository.
- Logging and cancellation: `log/slog`, `context.Context`, and `signal.NotifyContext`.

Do not add an external dependency when the standard library is sufficient. Before adding an unlisted direct dependency, explain why it is needed, check maintenance and license status, and ask the user for approval.

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
│   └── openai/                 # OpenAI protocol request/stream translation
├── provider/
│   └── deepseek/               # DeepSeek auth, models, defaults, compatibility
├── tool/                       # read, search, write, edit, exec implementations
├── session/                    # JSONL history, context views, compaction
└── config/                     # config and credential loading

testdata/                       # fixtures and golden inputs
```

Create packages only when implementation requires them; do not scaffold empty layers. Avoid vague catch-all packages such as `core`, `types`, `services`, `utils`, or `helpers`.

Dependency rules:

- `cmd/aice` stays minimal and delegates assembly and execution to `internal/app` and `internal/cli`.
- `agent` depends only on small AICE contracts, primarily `llm`; it must not import Cobra, Bubble Tea, provider SDKs, or concrete tools.
- `api/openai` translates between SDK/protocol data and `llm` types. Provider SDK types must not leak beyond the adapter.
- `provider/deepseek` supplies DeepSeek-specific configuration and compatibility behavior while reusing the OpenAI protocol adapter.
- Concrete tools implement interfaces defined by their consumer. The Agent Loop must not construct tools itself.
- `tui` consumes agent events and converts them to `tea.Msg`; the agent must never emit or import Bubble Tea types.
- Wire dependencies explicitly with constructors in the composition root. Do not use global registries, service locators, `init()` wiring, or a DI framework.

## LLM and Agent Contracts

- AICE owns the canonical `Message`, `ContentPart`, `ToolCall`, `ToolResult`, `Model`, `Usage`, `StopReason`, `Event`, and stream abstractions.
- Keep provider identity/configuration separate from API protocol adapters. OpenAI-compatible providers should reuse the protocol layer instead of duplicating a full client.
- Normalize text, reasoning, tool-call deltas, usage, finish reasons, errors, and cancellation without losing information.
- The Agent Loop is responsible for model calls, validated tool execution, tool-result history, continuation, maximum turn/tool-step limits, and terminal lifecycle events.
- Never execute an incomplete or invalid streamed tool call. Preserve partial assistant output on cancellation or provider failure when it is safe to do so.
- Execute tools sequentially by default. Parallel execution may be added later only for independent read-only tools with explicit ordering and cancellation tests.
- Use a faux provider and fake tools as the primary executable specification for event ordering and loop behavior. Unit tests must not require paid APIs or real credentials.
- A second independent protocol, such as Anthropic, is the eventual validation that the `llm` abstraction is genuinely provider-neutral. Do not generalize for hypothetical providers before that need exists.

## Concurrency and TUI Boundaries

- Pass `context.Context` as the first parameter through every model request, agent run, and tool execution. Never store a context in a struct or replace a caller context with `context.Background()` mid-flow.
- Every goroutine must have a clear owner, cancellation path, and wait/exit path. Avoid fire-and-forget work.
- Each agent run owns its event stream. The sender closes its channel; receivers never close it. All blocking sends must also select on `ctx.Done()`.
- Keep queues and buffers bounded. Do not use global channels or a permanent cross-run event bus.
- The TUI model is presentation state, not session truth. Session history, model context, and terminal viewport are separate concepts.
- Only Bubble Tea's update loop mutates UI state. Coalesce streaming deltas before expensive Markdown rendering instead of reparsing the full message for every token.
- Do not use nonexistent convenience APIs such as `tea.ChannelCmd`; wrap one channel receive in a `tea.Cmd` and register the next receive from `Update`.

## Tool and Workspace Safety

- Resolve every file operation against an explicit workspace root and reject path traversal and symlink escapes.
- Implement read/list/search before mutating tools. Writes, edits, and commands require a clear approval policy.
- The exec tool must use `exec.CommandContext` with executable and arguments separated. Do not pass model text to `bash -c` or `sh -c` by default.
- Restrict executable names, working directories, and environment overrides. Do not expose inherited secrets to tools.
- Bound time, stdout, stderr, and total captured output. Preserve exit status and distinguish a command failure from an internal tool failure.
- Ensure cancellation terminates the complete spawned process tree and does not leave background processes behind.
- Redact secrets, credentials, and sensitive prompt content from logs and persisted diagnostic data.

## Sessions and Compaction

- Persist the complete original session as append-only JSONL. Do not overwrite source history during compaction.
- Keep session storage, the context sent to the model, and the TUI viewport as separate representations.
- Run compaction only at a complete turn boundary. Never split an assistant tool call from its tool result or mutate history concurrently with an active run.
- A compacted context is a summary plus recent messages; it is a derived view, not a destructive rewrite.
- Start with usage accounting, context-limit protection, and manual compaction. Add automatic compaction only after the JSONL and recovery behavior are stable.

## Go Code Quality

- Prefer clear, idiomatic Go over patterns copied from TypeScript. Keep functions focused, handle errors early, and avoid premature abstractions.
- Package names are short, lowercase, singular, and specific. Do not create `util` or `helper` packages.
- Define small interfaces where they are consumed; accept interfaces and return concrete types.
- Use manual constructor injection. Avoid mutable package globals and side-effectful `init()` functions.
- Wrap errors with context using `%w`; use errors for expected failures and reserve panics for broken invariants.
- Use concrete types or generics instead of `any` when the data shape is known. Keep raw JSON at protocol/tool boundaries.
- Propagate timeouts and cancellation to all external calls. Bound retries and check the context between attempts.
- Comments explain intent, invariants, protocol quirks, and security decisions, not obvious syntax.
- Do not preserve backward compatibility with the abandoned TypeScript implementation unless the user explicitly requests a migration path.

## Implementation Order

Build vertical, testable slices in this order unless the user changes priorities:

1. Establish `go.mod`, the minimal command entry point, and AICE-owned LLM/agent contracts.
2. Add faux provider and fake tool tests for a text turn and one complete tool loop.
3. Implement the OpenAI-compatible adapter and DeepSeek provider, including reasoning, streamed tool calls, usage, errors, and cancellation.
4. Add safe read/list/search workspace tools.
5. Add Cobra print/non-interactive mode.
6. Add the Bubble Tea TUI and agent-event bridge.
7. Add JSONL sessions, recovery, and manual compaction.
8. Add write/edit/exec with approvals, process control, and security tests.

Keep structural changes, behavior changes, and imported upstream code in separate reviewable commits. Do not bulk-copy a Go port and then delete unwanted features in the same change.

## Verification Commands

- Documentation-only changes: run `git diff --check`.
- After Go code changes: format only modified Go files, then run `go test ./...` and `go vet ./...`.
- When `.golangci.yml` exists, also run `golangci-lint run ./...` and fix new findings rather than suppressing them.
- For concurrency, cancellation, channels, or shared-state changes, run `go test -race ./...`.
- If a test file changes, run the focused test while iterating, then the full unit suite before handoff.
- Mark real-provider tests with an integration build tag and run them explicitly. Default tests must use faux providers and must not read provider credentials.
- Verify CLI/TUI work through the actual user-facing command. Do not treat package tests alone as proof that interactive behavior works.
- Do not invent build, release, changelog, or publishing commands before the repository defines them.

## Git and Collaboration

- Multiple sessions may share this worktree. Preserve unrelated staged, unstaged, and untracked changes.
- Modify and stage only explicit files owned by the current task. Never use `git add .`, `git add -A`, `git stash`, `git reset --hard`, `git checkout .`, or force push.
- Never commit unless the user asks. Before committing, inspect `git status` and the exact staged diff.
- When asked to commit, follow the repository's existing short gitmoji/conventional subject style and keep each commit to one intent. Do not use Pi package scopes or release conventions.
- If a conflict touches a file not modified for the current task, stop and ask the user instead of resolving it speculatively.

## User Direction

The user's explicit request overrides this guide. If an override would remove intentional behavior, weaken a security boundary, add an excluded framework, or change the agreed architecture, explain the conflict and obtain confirmation before proceeding.
