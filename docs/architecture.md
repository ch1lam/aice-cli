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
- Built-in tools use the host process environment. An intrinsic execution gate
  (`internal/guard`) checks every tool call inline; stronger isolation is
  still external (container/VM). Product behavior of the gate is in [Tool
  execution and Sessions](execution-sessions.md#tool-execution-boundary).
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
| `internal/provider/{deepseek,opencode,openai,custom}` | Provider catalogs, credentials, defaults, compatibility |
| `internal/provider/custom` | OpenAI-compatible catch-all (`custom`); any model ID; default base URL `http://localhost:11434/v1` |
| `internal/tool` | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls` |
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
  frontends, persists each completed interaction as one Session turn, and
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
- Interfaces are defined by consumers. Constructors return concrete types.
- Use standard library packages when sufficient. A new direct dependency
  requires an explanation, maintenance/license review, and user approval.
  The guard's bash AST parsing uses `mvdan.cc/sh/v3` (MIT) because dangerous
  command detection needs a real shell syntax tree; the standard library has
  no equivalent, and the hand-written tokenizer is only a fallback when AST
  parsing fails. Skill frontmatter parsing uses `gopkg.in/yaml.v3` (MIT,
  user-approved) because Agent Skills `SKILL.md` files use YAML frontmatter
  and interoperability with that ecosystem requires a YAML 1.2 parser; the
  standard library has no YAML package.
- Imported code must record its repository and commit and preserve required
  license notices. AICE remains Apache-2.0.

## Deliberately out of scope

Do not prebuild plugin frameworks, ADK/Eino/LangChainGo/Genkit integration,
Node bridges, multi-agent orchestration, LSP, RPC/ACP, memory services,
databases, or extra sandbox managers — the intrinsic `internal/guard` gate
is the in-process approval mechanism. Add another only after a concrete
product need defines its smallest useful contract.

## Planned extensions and restraint

These are decided shapes for when a concrete product requirement appears.
They are executable rules for future maintainers. Do not prebuild the
packages, frameworks, or persistence types listed as forbidden.

Freedom is released in four places only:

1. concrete `[]agent.Tool` implementations;
2. linear appends inside `assembleSystemPrompt`;
3. run-scoped policy on `Guard` (persistent grants are a planned
   extension and must not be prebuilt);
4. another `Loop` constructed in `internal/app`.

Restraint stays on Loop control flow, Session record types, the `llm`
message union, no global registries, and no second persistence store.

### Skills

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
  diagnostic.
- Ship one built-in skill tool that reads the already-parsed `SKILL.md`
  body into the tool result on demand.
- At startup inject only a `name` + `description` list, not skill bodies.

Do not: hot-reload skills, add a new message `Role`, or insert Loop
middleware.

### MCP

When implementing MCP:

- Client only. Adapt each MCP tool to an ordinary `agent.Tool` and run it
  through the same `executeTool` path and `Guard`.
- Wiring must replace `interactiveSession.tools` and call
  `rebuildAgentLoop`.

Do not: run an MCP server, add an ACP/plugin bus, bypass `Guard`, or add a
new `ContentType` for MCP resources.

### Memory

When implementing memory:

- Store Markdown files in a dedicated directory. Global files live under
  `~/.aice/memory/`. A project-level memory directory must go through Trust.
- Inject a truncated index into the prompt; read bodies on demand through a
  tool.

Do not: write memory into Session JSONL, add a database or vector store, or
create a second transcript store.

### Subagents

When implementing subagents:

- Expose a `delegate` tool whose implementation nests `Loop.Run`.
- Give the child a tool whitelist, an independent empty context, and return
  a summary as `ToolResult` to the parent turn.
- Defer parallelism. When it is required, either make `executeTools`
  concurrent or contain concurrency inside the tool. Before that change,
  lock the unexported `sessionAllowed`, `sessionAllowedPaths`, and
  `sessionAllowedTools` maps and the `sessionCmdPrefixes` slice on
  `guard.Guard`; they currently have no mutex and are safe only because
  tools run serially.

Do not: promote `/btw` or `SideThreadManager` into a subagent runtime, adopt
a multi-agent orchestration framework, or persist a child JSONL.

### Plan mode

When implementing plan mode:

- Add a run-scoped read-only flag on `Guard` plus a `/plan` command.
- Switch the mode between runs. `SystemPrompt` is frozen for the duration of
  a run.
- Clear the flag after the user approves leaving plan mode.

Do not: add a Mode state-machine framework, make `ExitPlanMode` a Loop
primitive, or record plan text in the Session.
