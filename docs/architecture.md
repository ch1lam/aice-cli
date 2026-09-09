# Architecture

## Product boundary

AICE is a small coding harness, not an agent platform. It ships as one Go
module and binary, with one AICE process; host tools may spawn subprocesses.

## Design philosophy

Keep the next change understandable. The default is to add behavior in its
owning package and wire it through an existing boundary. An interface earns
its place when a real consumer needs substitution or testing; a package earns
its place when it owns a distinct responsibility. Neither a growing file nor
an upstream framework is by itself a reason to add another architectural layer.

Preserve these properties because they make failures and changes traceable:

- **Visible control flow:** one place decides what runs next and when it stops.
- **Explicit ownership:** a maintainer can identify who creates, mutates, cancels,
  and closes each resource.
- **Recoverable history:** summaries and presentation can be rebuilt without
  destroying the original interaction.
- **Local change:** provider quirks, UI behavior, and host execution remain at
  their boundaries.
- **Requirement-led extension:** a product need justifies new machinery; a
  possible future feature does not justify building it today.

These principles guide tradeoffs, not a frozen directory layout. Use the
[maintenance procedure](maintenance.md#resolving-discrepancies) when the intended
boundary and implementation disagree.

Durable design rules:

- AICE owns provider-neutral messages, Agent events, usage, tools, and loop
  semantics. Provider SDK types stop at protocol adapters.
- Provider catalogs own model capability facts. Canonical thinking inputs map
  to provider tokens through model metadata; application code collapses
  equivalent mappings into distinct choices and clamps requests, while
  adapters only encode protocol-specific shapes.
- Sessions are the append-only source of truth. Model context and the TUI
  viewport are derived views.
- The application owns conversation routing identity and propagates it through
  context using the provider-neutral LLM metadata helpers. Providers own its
  HTTP encoding; the Loop and protocol adapters do not select Session identity.
  See [OpenCode routing behavior](configuration.md).
- Built-in tools use the host process environment. An intrinsic execution gate
  (`internal/guard`) checks every tool call inline; stronger isolation is
  still external (container/VM). Product behavior of the gate is in [Tool
  execution and Sessions](execution-sessions.md#tool-execution-boundary).
- Built-ins and future replacements use the same consumer-owned interfaces.
- Dependencies are assembled explicitly in `internal/app`.

## Runtime flow

```text
cmd/aice
  -> internal/app (composition and lifecycle)
     -> internal/cli or internal/tui (user interface)
     -> internal/interaction (active-run input coordination)
     -> internal/agent -> internal/llm (Agent Loop and contracts)
        -> internal/guard (intrinsic execution gate, checked before every tool call)
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

Within the application, conversation state owns the Session store, derived
history, and accepted messages from the active interaction. It serializes
durable history updates separately from the short lock used by side-question
snapshots. The interactive coordinator freezes model settings and handles
workspace and command lifecycle; an active run owns its input mailbox and
execution. This keeps transcript publication and copying at one state owner
without exposing storage concerns to the frontend or Agent Loop.

## Package map

| Package | Ownership |
| --- | --- |
| `cmd/aice` | Minimal process entry point |
| `internal/buildinfo` | Build-stamped version shared by CLI, updates, and protocol client identity |
| `internal/app` | Composition root, lifecycle, prompt assembly, Sessions, Agent-event translation, interactive commands |
| `internal/cli` | Cobra commands, flags, validation, exit behavior |
| `internal/tui` | Bubble Tea presentation and interaction-event rendering |
| `internal/interaction` | Frontend-neutral active-run, event, command, state, and input-mailbox contracts |
| `internal/agent` | Agent Loop, retries, tool lifecycle, Agent events |
| `internal/media` | Shared image decoding, conversion, validation, resizing, original retention and coordinate descriptions |
| `internal/llm` | Canonical messages, models, usage, streams, context estimates |
| `internal/api/{anthropic,openairesponses,openaicompletions}` | Protocol translation around official SDKs |
| `internal/api/streamcore` | Protocol-neutral streaming mechanics shared by adapters |
| `internal/provider/{deepseek,opencode,kimi,moonshot,zhipu,openai,codex,custom}` | Provider catalogs, credentials, defaults, compatibility; `zhipu` owns separate API Platform and Coding Plan presets; `codex` owns ChatGPT OAuth; `custom` accepts arbitrary model IDs |
| `internal/tool` | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls`, `skill` |
| `internal/guard` | Intrinsic execution gate: file policies, permission gate, pathAccess mode (`allow`/`ask`/`block`), check Decision (`allow`/`ask`/`deny`) |
| `internal/session` | Versioned JSONL replay, tree navigation, compaction context |
| `internal/trust` | Protected-resource discovery and global Trust decisions |
| `internal/skill` | Agent Skill discovery, SKILL.md parse, source layering, embedded builtins |
| `internal/config` | Global settings, credentials, environment precedence |
| `internal/deps` | Verified ripgrep and Windows Git Bash provisioning |
| `internal/update` | Checksum-validated GitHub release updates |
| `internal/hostpath` | Host path membership, tilde expansion, slash-normalized display |
| `internal/jsonutil`, `internal/apitest` | Focused shared JSON and test infrastructure |

Create a package only when implementation requires it. Avoid vague packages
such as `core`, `types`, `services`, `utils`, or `helpers`.

## Dependency and ownership rules

- `cmd/aice` delegates assembly and execution to `internal/app`.
- `internal/cli` calls application capabilities through small interfaces.
- `internal/agent` depends on AICE contracts, primarily `internal/llm`; it
  never constructs tools or imports UI/provider/`guard` packages. It defines
  the `Guard` interface and consults it before each tool execution;
  `NewLoop` requires a non-nil `Guard` when tools are non-empty. The
  concrete `internal/guard` implementation is injected from `internal/app`.
- `internal/app` owns interactive run lifecycle, translates Agent events for
  frontends, persists each accepted source message before dependent effects, and
  wires the execution gate into the loop. See [Tool execution and
  Sessions](execution-sessions.md#tool-execution-boundary).
- Frontends depend on the application-owned active-run capability. They do not
  decide when an Agent run stops, promote input, or restart a run for follow-up.
- `internal/llm` defines thinking-map and clamping semantics without embedding
  provider or model catalogs.
- Provider packages select models and protocol adapters and own per-model
  thinking maps and format metadata. They implement `llm.Streamer` and must
  not import `internal/agent`. API packages alone translate those canonical
  values into official SDK or wire types; `streamcore` contains no
  model-specific reasoning policy.
- Interfaces are defined by consumers. Constructors normally return concrete
  types; adapter factories may return the consumer capability they select.
- Codex subscription login is an application command using the provider's
  OAuth client, shared by terminal auth commands and the TUI’s cancellable
  account-login flow. The app opens the browser; transient interaction events
  carry progress and manual authorization input. `internal/config` owns AICE's separate OAuth credential file,
  atomic replacement, and a bounded cross-process lock. Each model request
  rereads credentials under that lock and refreshes near expiry before sending.
  The Responses adapter owns Codex wire differences and encrypted reasoning
  replay. No external harness, subprocess agent, or additional runtime is used.
- Do not use mutable global service registries or `init()` wiring. Fixed,
  package-private dispatch tables are ordinary implementation data, not an
  extension mechanism; keep dependency construction in `internal/app`. Do not
  add service locators or DI frameworks.
- Use standard library packages when sufficient. A new direct dependency
  requires an explanation, maintenance/license review, and user approval.
  The guard's bash AST parsing uses `mvdan.cc/sh/v3` (MIT) because dangerous
  command detection needs a real shell syntax tree; the standard library has
  no equivalent, and the hand-written tokenizer is only a fallback when AST
  parsing fails. Skill frontmatter parsing uses `gopkg.in/yaml.v3` (MIT,
  user-approved) because Agent Skills `SKILL.md` files use YAML frontmatter
  and interoperability requires YAML parsing; the standard library has no
  YAML package.
  Read path normalization reuses the already-pinned `golang.org/x/text`
  module's `unicode/norm` package (Go Authors, BSD-3-Clause) as a direct import.
  The standard library has no Unicode normalization package; maintained Unicode
  tables replace a partial handwritten decomposition map. Candidate selection remains in `internal/tool`.
  Image decoding uses `golang.org/x/image` (Go-maintained supplementary image
  libraries, BSD-3-Clause, user-approved) for BMP and WebP; the standard library
  has no decoders for those formats. PNG/JPEG/GIF use the standard library.
  Conversion and resource limits stay in `internal/media`; no host converter
  or additional runtime is required.
- Imported code must record its repository and commit and preserve required
  license notices. AICE remains Apache-2.0.

## Skills

`internal/skill` discovers and parses Agent Skills. A skill is a directory
with `SKILL.md` (YAML frontmatter plus Markdown). All sources are the same
resource on one scan/parse/validation path; `Source` is only a metadata
label for display and same-name conflict ordering.

- Layout (vendor-neutral, matching `npx skills add`): project
  `<workspace>/.agents/skills/<name>/SKILL.md` and user
  `~/.agents/skills/<name>/SKILL.md`. Scan is one level of children under a
  caller-supplied root (`fs.FS`); the package does not choose roots.
- Sources: `builtin` (go:embed in `internal/skill`), `user`, `project`.
  Same-name priority is project > user > builtin. Builtin skills get no
  parse exemption, auto-activation, or display preference.
- Project `.agents/skills/` is Trust-gated (`trust.SkillsDir`). User-global
  skills are not. See [Protected project
  resources](project-trust.md#protected-project-resources).
- Frontmatter is parsed with `gopkg.in/yaml.v3`. Name and description are
  required; other spec fields are tolerated and ignored. Validation is
  lenient: format issues warn and still load; missing name/description,
  missing frontmatter, or unparseable YAML skip that skill with an error
  diagnostic. Files must be regular, valid UTF-8, and at most 256 KiB;
  invalid or oversized files are skipped.
- Ship one built-in `skill` tool that returns the already-parsed `SKILL.md`
  body on demand. `internal/tool` does not import `internal/skill`; `internal/app`
  maps catalog entries into `tool.SkillEntry`.
- At startup inject only a `name` + `description` list, not skill bodies.
- Skill directories with a host path (`Dir` non-empty) are passed to
  `guard.Config.ReadOnlyRoots` so `read`/`grep`/`find`/`ls` can load bundled
  resources without path-access prompts. `write`/`edit` are not granted.
  Product behavior of the gate is in [Tool execution and
  Sessions](execution-sessions.md#tool-execution-boundary). `internal/app`
  wires those directories at startup. Assembly order is trust decision, skill
  discovery, tool construction, prompt assembly, then guard.

Do not: hot-reload skills, add a new message `Role`, or insert Loop
middleware.

## Planned extensions and restraint

These capabilities are not implemented. Their status records product scope;
implementation requires a concrete use case and resolution of the open decisions.
Use existing boundaries and update the owning guide when a capability ships.

| Capability | Status | Design boundary |
| --- | --- | --- |
| Plan mode | Required, not implemented | Enforce allowed actions in Guard; app owns mode transitions and UI commands |
| Subagents | Required, not implemented | Reuse the Agent Loop through an app-wired tool with explicit child ownership |
| Memory | Required product capability, strategy undecided; optional use | Project context remains primary; retention, scope, and retrieval need a concrete design |
| MCP | Optional, no current requirement | If needed, adapt client tools to the existing Tool and Guard boundary |

No agent framework, plugin bus, LSP, RPC/ACP layer, Node bridge, database,
second transcript store, or additional sandbox manager is needed by default.
A concrete requirement must explain why existing boundaries are insufficient
before adding one. The in-process Guard and external host isolation remain
separate concerns.

Plan mode must enforce restrictions in Guard and define permitted actions, exit
approval, and transitions relative to an active run's frozen prompt. Subagents
need explicit child cancellation, context, permission inheritance, usage and
failure handling; concurrent children require a review of shared Guard/tool
state. The main Session remains the transcript, and `/btw` remains tool-free.
Memory needs user-controlled scope, retention and retrieval, with Trust for
project-owned loading. None requires a new Loop primitive or transcript store
by default.
