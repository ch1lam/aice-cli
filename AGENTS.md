# AICE Development Guide

AICE is a batteries-included coding agent implemented in pure Go, inspired by Pi's design philosophy. Current product priority: **Session management** — an append-only JSONL tree with restart recovery, branch navigation, backtracking, and branch-local manual compaction.

The user's explicit request overrides this guide. If a request conflicts with an invariant below, explain the conflict and confirm the intended change, then update this guide before implementing the new direction.

## Non-negotiable Invariants

- Single Go module, single AICE binary. Built-in tools may spawn host executables (Bash, ripgrep); no daemon or extra AICE service.
- Standard library first. Any new direct dependency requires an explanation, maintenance/license review, and explicit user approval.
- The removed TypeScript implementation is not a compatibility target. Never restore Node.js/npm, Vercel AI SDK, Ink, oclif, or old session/config formats.
- Do not add ADK, Eino, LangChainGo, Genkit, Node bridges, multi-agent orchestration, LSP, RPC/ACP, or memory services before a concrete product requirement justifies them.
- Sessions are append-only JSONL tree nodes with stable IDs and parent IDs. Never overwrite source history, delete branches, or introduce a second transcript store or mutable database representation. Compact only at complete turn boundaries.
- The Agent Loop never constructs tools; concrete tools implement interfaces defined by their consumers. The agent never imports Bubble Tea; the TUI is presentation state, never session truth.
- AICE has no intrinsic approval system. Built-in tools run with the process's permissions; isolation and policy belong to an explicitly selected execution environment, never hard-coded into the Agent Loop.
- Wire dependencies explicitly with constructors in `internal/app`. No global registries, service locators, or `init()` wiring.
- No fixed `MaxTurns`/`MaxToolSteps`. Never execute an incomplete or invalid streamed tool call.

## Workflow

- Read files in full before wide-ranging changes, before editing an uninspected file, and for investigations or audits.
- Make surgical changes that trace to the request. Keep structural changes, behavior changes, and imported upstream code in separate commits.
- Karpathy guidelines apply: think before coding, simplicity first, goal-driven execution with per-step verification. See docs/go-quality.md.
- After Go changes: format modified files, then `go test ./...` and `go vet ./...`. Run `go test -race ./...` for concurrency changes.
- Never commit unless asked. Preserve unrelated changes. Never use `git add .`, `git stash`, `git reset --hard`, `git checkout .`, or force push.

## Docs Index

Read the relevant doc for the task; details live here, not in AGENTS.md.

| Doc | Contents |
| --- | --- |
| docs/architecture.md | Project direction, tech stack, intended structure, dependency rules, architecture correction order |
| docs/contracts.md | LLM and Agent message/tool contracts, Agent Loop rules, concurrency and TUI boundaries |
| docs/execution-sessions.md | Tool execution and sandbox boundaries, sessions and compaction |
| docs/go-quality.md | Go code quality standards, Karpathy guidelines |
| docs/collaboration.md | Verification commands, git and collaboration rules |
| docs/configuration.md | Settings, environment variables, credentials, interactive slash commands |

## User Direction

The user's explicit request overrides this guide. If a request appears to conflict with an established invariant or architecture decision, explain the conflict and confirm the intended change. Once the user explicitly changes that decision, update this guide before implementing the new direction.
