# AICE Development Guide

AICE is a small coding harness: one Go binary, an explicit Agent Loop,
replaceable provider/tool/UI boundaries, and append-only Session history.
Maintainability means a maintainer can trace a request, identify who owns its
state, and change one behavior without rebuilding the surrounding system.

## Start here

1. Read [Architecture](docs/architecture.md) for the design philosophy and
   package boundaries, then use the task map below. Read
   [Maintenance](docs/maintenance.md) when investigating drift or taking over
   unfamiliar work; it links code entry points and known discrepancies.
2. Inspect `git status --short`. Preserve unrelated work. Read affected files
   in full and trace their callers and tests before editing or concluding that
   behavior is wrong; search snippets are navigation, not evidence by themselves.
3. State the intended behavior, its owner, and a focused way to verify it.
   Prefer a local change through an existing boundary. Broaden only when the
   evidence requires it.
4. Update the owning document with the implementation and run the checks in
   [Verification and collaboration](docs/collaboration.md). Report what changed,
   what was verified, and any unresolved discrepancy.

The user's explicit request overrides this guide. When it changes a durable
architecture decision, explain the conflict and confirm the intended direction
if the request has not already settled it. Update the owning document and this
index where affected. Routine fixes within the existing design need no new
design approval. Do not silently rewrite a contract to match a suspected bug;
use the [discrepancy procedure](docs/maintenance.md#resolving-discrepancies).

## Task map

Each detailed rule has one owning document. This file keeps short guardrails
and routes; README files may summarize product behavior and link to details.

| Task area | Read |
| --- | --- |
| Product overview and quickstart | [README.md](README.md) and [README-zh.md](README-zh.md); keep both in sync |
| Installation, helper binaries, source builds, updates | [Installation](docs/installation.md) |
| Providers, models, reasoning, credentials, flags, Skills, TUI input | [Configuration](docs/configuration.md) |
| Project Trust, prompt precedence, protected resources, `/init` | [Project Trust](docs/project-trust.md) |
| Tool permissions, Sessions, `/new`, branches, recovery, compaction | [Execution and Sessions](docs/execution-sessions.md) |
| Package ownership, dependencies, extension boundaries | [Architecture](docs/architecture.md) |
| Messages, Agent Loop, events, concurrency, TUI boundaries | [Runtime contracts](docs/contracts.md) |
| Go implementation and review | [Go quality](docs/go-quality.md) |
| Verification, CI, git and collaboration | [Collaboration](docs/collaboration.md) |
| Code entry points, conflicting evidence, known deviations | [Maintenance](docs/maintenance.md) |
| Harbor evaluation adapter | [Harbor integration](integrations/harbor/README.md) |

## Guardrails

- **Keep the harness small.** One Go module and AICE binary; host helpers are
  allowed. No extra AICE service, speculative framework, or restored TypeScript
  runtime. [Architecture](docs/architecture.md#product-boundary)
- **Make dependencies explicit.** Consumer-owned interfaces, constructor wiring
  in `internal/app`, no mutable service registries or `init()` wiring. Standard
  library first; new direct dependencies require a reason, maintenance/license
  review, and explicit user approval.
  [Ownership rules](docs/architecture.md#dependency-and-ownership-rules)
- **Keep control flow in the Agent Loop.** The loop owns tools, steering,
  follow-up, retries, and stopping. No fixed `MaxTurns`/`MaxToolSteps`; never
  execute invalid or incomplete streamed tool calls. UI and concrete tool/SDK
  dependencies stay out of the loop. [Contracts](docs/contracts.md#agent-loop)
- **Keep history recoverable.** Session JSONL is the only durable transcript;
  turns have stable IDs and parents. Never rewrite source history or delete
  branches. Compact only at complete interaction boundaries; context and UI are
  derived views. [Sessions](docs/execution-sessions.md#sessions)
- **Keep authority explicit.** Every tool execution goes through the injected
  Guard; non-empty tool sets require one. Unknown tools ask, non-interactive
  asks fail closed, and `--yolo` must not lift a deny. Trust controls project
  instruction loading, not host isolation. Check documented deviations before
  extending these paths. [Execution boundary](docs/execution-sessions.md#tool-execution-boundary)
- **Extend only for a concrete need.** Use the existing boundaries; review any
  change to Loop control flow, message/Session types, permission scope, or state
  ownership. [Extension constraints](docs/architecture.md#planned-extensions-and-restraint)

## Optional Go skills

This repository must be maintainable without a particular model, harness,
plugin, or locally installed skill library. [Go quality](docs/go-quality.md)
and the documents above are sufficient working instructions.

When available, use `samber/cc-skills-golang@golang-how-to` to select relevant
skills for the task. Load only what helps the affected area. Missing skills
do not block work and do not require installing tools or dependencies.
Repository-specific decisions take precedence over generic skill examples.
