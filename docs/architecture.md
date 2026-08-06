# Architecture

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
- Providers: DeepSeek through Anthropic Messages and OpenAI Responses adapters, and OpenCode Go through an OpenAI Chat Completions adapter. All three use official provider SDKs with configurable base URLs.
- Persistence: append-only JSONL sessions. Use standard library packages before adding storage frameworks or databases.
- Execution: built-in tools use the host process environment by default. The built-in `grep` tool requires `ripgrep` (`rg`) on `PATH` and has no pure-Go fallback. Containers, sandboxes, or replaceable execution environments may provide stronger isolation.
- Configuration: Viper-backed global settings, `AICE_*` environment variables, and the `.aice` directory. Provider, model, and thinking belong only in `~/.aice/settings.json`; credentials belong only in the global auth file or process environment. Do not read or write model configuration from a project settings file.
- Logging and cancellation: `log/slog`, `context.Context`, and `signal.NotifyContext`.

Do not add an external dependency when the standard library is sufficient. Before adding an unlisted direct dependency, explain why it is needed, check maintenance and license status, and ask the user for approval. Viper is approved for environment-over-global configuration resolution; keep global settings persistence and credential handling in AICE-owned code. `ripgrep` is an approved runtime dependency for the built-in `grep` tool because its search performance and ignore semantics are part of the intended behavior; do not restore the removed pure-Go search implementation as a fallback.

## Intended Project Structure

```text
cmd/
└── aice/
    └── main.go                 # minimal process entry point

internal/
├── app/                        # composition root and lifecycle
├── cli/                        # Cobra commands and non-interactive output
├── tui/                        # Bubble Tea model, update, view, event bridge
├── agent/                      # Agent Loop, lifecycle events, Tool contract
├── llm/                        # AICE messages, content, models, usage, stream contract
├── api/
│   └── anthropic/              # Anthropic protocol request/stream translation
├── provider/
│   └── deepseek/               # DeepSeek auth, models, defaults, compatibility
├── tool/                       # read, search, write, edit, bash implementations
├── session/                    # JSONL history, context views, compaction
├── trust/                      # project trust store, paths, resolution
└── config/                     # config and credential loading

testdata/                       # fixtures and golden inputs
```

Create packages only when implementation requires them; do not scaffold empty layers. Avoid vague catch-all packages such as `core`, `types`, `services`, `utils`, or `helpers`.

Dependency rules:

- `cmd/aice` stays minimal and delegates process assembly and execution to `internal/app`.
- `internal/app` is the composition root. It owns lifecycle and assembles CLI behavior, models, tools, sessions, execution environments, and the TUI.
- `internal/cli` owns only the command surface and invokes application capabilities through small consumer-defined interfaces.
- `agent` depends only on small AICE contracts, primarily `llm`; it must not import Cobra, Bubble Tea, provider SDKs, or concrete tools.
- `api/anthropic`, `api/openairesponses`, and `api/openaicompletions` translate between SDK/protocol data and `llm` types. Provider SDK types must not leak beyond the adapter.
- `provider/deepseek` supplies DeepSeek-specific configuration and compatibility behavior while reusing the Anthropic Messages and OpenAI Responses protocol adapters.
- `provider/opencode` supplies OpenCode Go configuration and compatibility behavior while reusing the OpenAI Chat Completions protocol adapter.
- Concrete tools implement interfaces defined by their consumer. The Agent Loop must not construct tools itself.
- `tui` consumes agent events and converts them to `tea.Msg`; the agent must never emit or import Bubble Tea types.
- Built-in implementations use the same explicit contracts intended for future replacements and extensions. Do not give built-ins privileged access to core internals.
- Wire dependencies explicitly with constructors in the composition root. Do not use global registries, service locators, `init()` wiring, or a DI framework.

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
