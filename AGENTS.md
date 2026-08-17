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

Use this table to route each task directly to its source of truth:

| Task area | Read |
| --- | --- |
| Product overview and quickstart | [`README.md`](README.md) |
| Installation, helper binaries, source builds, updates | [`docs/installation.md`](docs/installation.md) |
| Providers, models, reasoning, credentials, CLI/TUI commands | [`docs/configuration.md`](docs/configuration.md) |
| Project Trust, prompt precedence, protected files, `/init` | [`docs/project-trust.md`](docs/project-trust.md) |
| Tool permissions, Sessions, branches, recovery, compaction | [`docs/execution-sessions.md`](docs/execution-sessions.md) |
| Runtime boundaries, package ownership, dependencies | [`docs/architecture.md`](docs/architecture.md) |
| LLM messages, Agent Loop, events, concurrency, TUI | [`docs/contracts.md`](docs/contracts.md) |
| Go engineering rules | [`docs/go-quality.md`](docs/go-quality.md) |
| Verification and git workflow | [`docs/collaboration.md`](docs/collaboration.md) |

## Non-negotiable Invariants

- One Go module and one AICE binary. Built-in tools may spawn host executables
  such as Bash and ripgrep; do not add a daemon or extra AICE service.
- Standard library first. Any new direct dependency requires a need,
  maintenance/license review, and explicit user approval.
- The removed TypeScript implementation is not a compatibility target. Never
  restore Node.js/npm, Vercel AI SDK, Ink, oclif, or old session/config formats.
- Do not add ADK, Eino, LangChainGo, Genkit, Node bridges, multi-agent
  orchestration, LSP, RPC/ACP, or memory services without a concrete product
  requirement.
- Sessions are append-only JSONL tree nodes with stable IDs and parent IDs.
  Never overwrite source history, delete branches, or add a second transcript
  store or mutable database. Compact only at complete turn boundaries.
- The Agent Loop never constructs tools. Concrete tools implement interfaces
  defined by their consumers. The agent never imports Bubble Tea; the TUI is
  presentation state, not Session truth.
- The Agent Loop owns both loop levels: tools and steering continue the current
  interaction, while follow-up continues the same Agent run only at a natural
  stop boundary. Frontends never restart a run to consume follow-up. Persist
  each completed interaction as one Session turn; Session records do not
  dictate Agent-run lifecycle.
- AICE has no intrinsic approval system. Built-in tools inherit process
  permissions; isolation belongs to an explicitly selected external execution
  environment, never the Agent Loop.
- Project Trust gates only workspace-root `AGENTS.md`, `.aice/SYSTEM.md`, and
  `.aice/APPEND_SYSTEM.md`. It is not a sandbox. `.aice/sessions` never
  triggers Trust; provider, model, thinking, and credentials remain global.
- Prompt precedence is: trusted project `.aice/SYSTEM.md`, global
  `~/.aice/SYSTEM.md`, built-in default; then trusted project `AGENTS.md`;
  then project or global `APPEND_SYSTEM.md`. Project files use confined
  `os.Root` reads, must be regular UTF-8 files under 64 KiB, and never enter
  compaction requests.
- Wire dependencies explicitly with constructors in `internal/app`. No global
  registries, service locators, DI frameworks, or `init()` wiring.
- No fixed `MaxTurns` or `MaxToolSteps`. Never execute an incomplete or invalid
  streamed tool call.

## Workflow and Verification

- Think before coding, prefer the smallest solution, make surgical changes,
  and verify each step. See [`docs/go-quality.md`](docs/go-quality.md).
- Keep structural changes, behavior changes, and imported upstream code in
  separate commits.
- Documentation-only changes: run `git diff --check` and verify links.
- Go changes: format modified files, then run `go test ./...` and
  `go vet ./...`; add `go test -race ./...` for concurrency changes.
- Follow the complete command and collaboration matrix in
  [`docs/collaboration.md`](docs/collaboration.md).
- Never commit unless asked. Preserve unrelated changes. Never use
  `git add .`, `git add -A`, `git stash`, `git reset --hard`,
  `git checkout .`, or force push.

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
