# Architecture

## Product boundary

AICE is a small coding harness, not an agent platform. It ships as one Go
module, binary, and process. Pi is a semantic reference for the Agent Loop,
messages, tools, events, and Session truth; it is not a source-tree template
or a compatibility target.

Durable design rules:

- AICE owns provider-neutral messages, Agent events, usage, tools, and loop
  semantics. Provider SDK types stop at protocol adapters.
- Provider catalogs own model capability facts. Canonical thinking inputs map
  to provider tokens through model metadata; application code collapses
  equivalent mappings into distinct choices and clamps requests, while
  adapters only encode protocol-specific shapes.
- Sessions are the append-only source of truth. Model context and the TUI
  viewport are derived views.
- Built-in tools use the host process environment. Isolation is external.
- Built-ins and future replacements use the same consumer-owned interfaces.
- Dependencies are assembled explicitly in `internal/app`.

The removed TypeScript implementation and its Node.js, npm, Vercel AI SDK,
Ink, oclif, Session, and configuration formats are not compatibility targets.

## Runtime flow

```text
cmd/aice
  -> internal/app (composition and lifecycle)
     -> internal/cli or internal/tui (user interface)
     -> internal/interaction (active-run input coordination)
     -> internal/agent -> internal/llm (Agent Loop and contracts)
     -> internal/provider -> internal/api (provider and protocol adapters)
     -> internal/tool (host-executed tools)
     -> internal/session (append-only JSONL tree)
     -> internal/trust + internal/config (startup inputs)
```

The Agent Loop does not know Cobra, Bubble Tea, concrete tools, Session files,
or provider SDKs. The application prepares one active run and connects its
UI-neutral input mailbox to the Agent Loop's steering and follow-up sources.
The TUI is display state: it submits inputs to that capability, mirrors pending
inputs for presentation, and never owns delivery semantics or writes Session
truth. A future GUI must use the same application-owned active-run boundary.

## Package map

| Package | Ownership |
| --- | --- |
| `cmd/aice` | Minimal process entry point |
| `internal/app` | Composition root, lifecycle, prompt assembly, Sessions, Agent-event translation, interactive commands |
| `internal/cli` | Cobra commands, flags, validation, exit behavior |
| `internal/tui` | Bubble Tea presentation and interaction-event rendering |
| `internal/interaction` | Frontend-neutral active-run, event, command, state, and input-mailbox contracts |
| `internal/agent` | Agent Loop, retries, tool lifecycle, Agent events |
| `internal/llm` | Canonical messages, models, usage, streams, context estimates |
| `internal/api/{anthropic,openairesponses,openaicompletions}` | Protocol translation around official SDKs |
| `internal/api/streamcore` | Protocol-neutral streaming mechanics shared by adapters |
| `internal/provider/{deepseek,opencode,openai}` | Provider catalogs, credentials, defaults, compatibility |
| `internal/tool` | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls` |
| `internal/session` | Versioned JSONL replay, tree navigation, compaction context |
| `internal/trust` | Protected-resource discovery and global Trust decisions |
| `internal/config` | Global settings, credentials, environment precedence |
| `internal/deps` | Verified ripgrep and Windows Git Bash provisioning |
| `internal/update` | Checksum-validated GitHub release updates |
| `internal/jsonutil`, `internal/apitest` | Focused shared JSON and test infrastructure |

Create a package only when implementation requires it. Avoid vague packages
such as `core`, `types`, `services`, `utils`, or `helpers`.

## Dependency and ownership rules

- `cmd/aice` delegates assembly and execution to `internal/app`.
- `internal/cli` calls application capabilities through small interfaces.
- `internal/agent` depends on AICE contracts, primarily `internal/llm`; it
  never constructs tools or imports UI/provider packages.
- `internal/app` owns interactive run lifecycle, translates Agent events for
  frontends, and persists each completed interaction as one Session turn.
- Frontends depend on the application-owned active-run capability. They do not
  decide when an Agent run stops, promote input, or restart a run for follow-up.
- `internal/llm` defines thinking-map and clamping semantics without embedding
  provider or model catalogs.
- Provider packages select models and protocol adapters and own per-model
  thinking maps and format metadata. API packages alone translate those
  canonical values into official SDK or wire types; `streamcore` contains no
  model-specific reasoning policy.
- Interfaces are defined by consumers. Constructors return concrete types.
- Use standard library packages when sufficient. A new direct dependency
  requires an explanation, maintenance/license review, and user approval.
- Imported code must record its repository and commit and preserve required
  license notices. AICE remains Apache-2.0.

## Deliberately out of scope

Do not prebuild plugin frameworks, ADK/Eino/LangChainGo/Genkit integration,
Node bridges, multi-agent orchestration, LSP, RPC/ACP, memory services,
databases, or sandbox managers. Add one only after a concrete product need
defines its smallest useful contract.
