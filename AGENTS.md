# AICE Development Guide

AICE is a batteries-included coding agent implemented as one Go binary. Its
core is an explicit Agent Loop with replaceable provider, tool, TUI, and
persistence boundaries. Sessions are append-only JSONL trees with recovery,
branch navigation, backtracking, and branch-local automatic/manual compaction.

The user's explicit request overrides this guide. If it changes an invariant
or architecture decision, explain the conflict, confirm the new direction,
update this file and the relevant durable document, then implement it.

## Start Here

- Read the relevant document before changing its area; read files in full
  before editing an uninspected file or making audit-wide conclusions.
- Keep product behavior in user docs, architecture decisions in `docs/`, and
  only repository-wide operating rules here.
- Remove temporary implementation plans after their completed decisions have
  been absorbed into durable documentation; Git history records how they landed.

## Documentation Map

Use this table to route each task directly to its source of truth. State each
fact in one document; other files should link rather than restating details.

| Task area | Read |
| --- | --- |
| Product overview and quickstart | [`README.md`](README.md) (keep [`README-zh.md`](README-zh.md) in sync) |
| Installation, helper binaries, source builds, `aice update` | [`docs/installation.md`](docs/installation.md) |
| Providers, models, reasoning, credentials, CLI flags, Agent Skills, TUI commands except Session/compaction | [`docs/configuration.md`](docs/configuration.md) |
| Project Trust, prompt precedence, protected resources, `/init` | [`docs/project-trust.md`](docs/project-trust.md) |
| Tool permissions, execution gate, Sessions, branches, recovery, compaction | [`docs/execution-sessions.md`](docs/execution-sessions.md) |
| Session CLI/TUI commands (`aice session`, `aice compact`, `/session`, `/tree`, `/checkout`, `/compact`) | [`docs/execution-sessions.md`](docs/execution-sessions.md#resume-and-navigate) |
| Runtime boundaries, package ownership, dependencies, planned extensions | [`docs/architecture.md`](docs/architecture.md) |
| LLM messages, Agent Loop, events, concurrency, TUI | [`docs/contracts.md`](docs/contracts.md) |
| Go engineering rules | [`docs/go-quality.md`](docs/go-quality.md) |
| Verification commands and git workflow | [`docs/collaboration.md`](docs/collaboration.md) |

## Non-negotiable Invariants

Each bullet is one grep-able fact plus its source of truth. Do not copy
product details from those documents into this file.

- One Go module and one AICE binary. Built-in tools may spawn host
  executables such as Bash and ripgrep; do not add a daemon or extra AICE
  service. See [`docs/architecture.md`](docs/architecture.md).
- Standard library first. Any new direct dependency requires a need,
  maintenance/license review, and explicit user approval. See
  [`docs/architecture.md`](docs/architecture.md).
- The removed TypeScript implementation is not a compatibility target. Never
  restore Node.js/npm, Vercel AI SDK, Ink, oclif, or old session/config
  formats. See [`docs/architecture.md`](docs/architecture.md).
- Do not add ADK, Eino, LangChainGo, Genkit, Node bridges, multi-agent
  orchestration, LSP, RPC/ACP, or memory services without a concrete product
  requirement. When that requirement exists, follow [Planned extensions and
  restraint](docs/architecture.md#planned-extensions-and-restraint).
- Sessions are append-only JSONL tree nodes with stable IDs and parent IDs.
  Never overwrite source history, delete branches, or add a second transcript
  store or mutable database. Compact only at complete turn boundaries. See
  [`docs/execution-sessions.md`](docs/execution-sessions.md).
- The Agent Loop never constructs tools. Concrete tools implement interfaces
  defined by their consumers. The agent never imports Bubble Tea; the TUI is
  presentation state, not Session truth. See
  [`docs/contracts.md`](docs/contracts.md) and
  [`docs/architecture.md`](docs/architecture.md).
- The Agent Loop owns both loop levels: tools and steering continue the
  current interaction; follow-up continues the same Agent run only at a
  natural stop boundary. Frontends never restart a run to consume follow-up.
  Persist each completed interaction as one Session turn. See
  [`docs/contracts.md`](docs/contracts.md).
- Every tool call is checked inline by `internal/guard` before it runs.
  Unknown tool names default to `ask` (non-interactive: `deny`, unless
  `--yolo`). `--yolo` upgrades `ask` to `allow` and does not lift `deny`.
  A Loop with a non-empty tool set must receive a `Guard`. Path-access
  mode is `allow`/`ask`/`block`; check `Decision` is `allow`/`ask`/`deny`.
  Run-scoped grants (Allow … for this run) are current-run, memory-only.
  The loop never constructs the gate; `internal/app` injects it. See [Tool
  execution and Sessions](docs/execution-sessions.md#tool-execution-boundary).
- Project Trust gates only workspace-root `AGENTS.md`, `.aice/SYSTEM.md`,
  `.aice/APPEND_SYSTEM.md`, and the project `.agents/skills` directory. It
  is not a sandbox. `.aice/sessions` never triggers Trust; provider, model,
  thinking, and credentials remain global.
  See [`docs/project-trust.md`](docs/project-trust.md).
- Prompt precedence is trusted project `.aice/SYSTEM.md`, else global
  `~/.aice/SYSTEM.md`, else built-in default; then trusted project
  `AGENTS.md`; then trusted project `.aice/APPEND_SYSTEM.md`, otherwise
  global `~/.aice/APPEND_SYSTEM.md`. See [Prompt
  assembly](docs/project-trust.md#prompt-assembly).
- Wire dependencies explicitly with constructors in `internal/app`. No
  global registries, service locators, DI frameworks, or `init()` wiring.
  See [`docs/architecture.md`](docs/architecture.md).
- No fixed `MaxTurns` or `MaxToolSteps`. Never execute an incomplete or
  invalid streamed tool call. See
  [`docs/contracts.md`](docs/contracts.md).

## Workflow and Verification

- Think before coding, prefer the smallest solution, make surgical changes,
  and verify each step. See [`docs/go-quality.md`](docs/go-quality.md).
- Keep structural changes, behavior changes, and imported upstream code in
  separate commits.
- Verification commands, git safety rules, and collaboration constraints
  live in [`docs/collaboration.md`](docs/collaboration.md).

## Go Skills

Before every Go coding, design, review, debugging, troubleshooting,
refactoring, or setup task, load
`samber/cc-skills-golang@golang-how-to` first.

For design and coding tasks in this repository, also always apply:

- `golang-code-style`, `golang-naming`, `golang-error-handling`
- `golang-structs-interfaces`, `golang-design-patterns`, `golang-refactoring`
- `golang-testing`, `golang-troubleshooting`, `golang-safety`, `golang-security`
- `golang-concurrency`, `golang-context`
- `golang-cli`, `golang-spf13-cobra`, `golang-spf13-viper`

All short names above belong to `samber/cc-skills-golang`.
