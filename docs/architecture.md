# Architecture

## Product boundary

AICE currently ships as a small coding harness: one Go module and binary,
with one AICE process; host tools may spawn subprocesses. This document describes
implemented boundaries. The [Roadmap](../ROADMAP.md) owns future product direction,
including additional interfaces and cloud operation.

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

## Runtime flow

```text
cmd/aice → internal/app (composition and lifecycle)
  UI:       cli / tui → interaction contracts → app operations
  Run:      agent → llm contracts → provider / api adapters
  Tools:    agent → injected Guard → built-in tools / run-local MCP catalog
  History:  synchronous recorder → app conversation → session JSONL
  Startup:  config + trust + skill → frozen configuration and prompt
```

The arrows describe calls, not imports. `agent` imports only `llm` among AICE
packages; its Guard, recorder and catalog are injected consumer-owned interfaces.
The Loop owns tool execution, steering, follow-up, retries and stopping. TUI owns
editing, local navigation and controller scheduling through `interaction`;
it does not own execution or durable history. See [Runtime contracts](contracts.md).

`app` freezes settings for each Run and owns resource preparation and cleanup.
Its conversation state owns the writable Session, paired history and accepted
pending inputs. Durable writes are serialized separately from short side-snapshot
reads. Compaction and branch changes rebuild derived context; ordinary appends
publish new complete message groups. The history picker caches immutable prose,
not writable stores or another transcript. See [Sessions](execution-sessions.md).

Settings, slash commands and login call application operations. Config owns
source precedence and locked partial writes; app owns prepare/save/publish and
resource revisions; TUI owns drafts and display. External setup and credential
writes can partially succeed, so their outcomes remain distinct from preference
commits. See [Configuration](configuration.md).

Optional capabilities retain their own owners:

- Web: app selects a source through the pure `web` resolver and constructs its
  backend. Provider wire data stops in the adapter; results use `evidence`.
  See [Web](web.md).
- MCP: app owns authorized reusable connections; each Run borrows them through
  a frozen catalog. The Loop loads complete schemas at tool-pair boundaries.
  Guard validates bound identities before dispatch; Session retains bounded
  results for local readback. See [MCP](mcp.md).
- Computer Use: app injects a managed MCP entry; `desktop` owns pinned native
  admission, sessions and frozen control/image capabilities. Cua owns targeting,
  tokens, capture and input semantics. The model uses native schemas/results and
  explicit observations; AICE has no second desktop action loop.
  See [Computer Use](desktop.md#managed-mcp-boundary).
- Browser: app owns process-scoped connection and helper lifecycle; the Loop
  remains unaware of browser implementation. See [Browser](browser.md).

## Package map

| Package | Ownership |
| --- | --- |
| `cmd/aice` | Minimal process entry point |
| `internal/buildinfo` | Build-stamped version shared by CLI, updates, and protocol client identity |
| `internal/app` | Composition root, lifecycle, prompt assembly, Sessions, Agent-event translation, interactive commands |
| `internal/cli` | Cobra commands, flags, validation, exit behavior |
| `internal/tui` | Bubble Tea presentation and interaction-event rendering |
| `internal/interaction` | Frontend-neutral active-run, event, command, state, question, and input-mailbox contracts |
| `internal/agent` | Agent Loop, retries, tool lifecycle, Agent events |
| `internal/media` | Shared image decoding, conversion, validation, resizing, original retention and coordinate descriptions |
| `internal/mcpclient` | One stdio/HTTP transport: bounded discovery/results, invalidation, cancellation and cleanup; SDK boundary |
| `internal/mcpauth` | OAuth discovery, registration, PKCE exchange and refresh protocol; app/config own user flow and persistence |
| `internal/llm` | Canonical messages, models, usage, streams, context estimates |
| `internal/api/{anthropic,openairesponses,openaicompletions}` | Protocol translation around official SDKs |
| `internal/api/streamcore` | Protocol-neutral streaming mechanics shared by adapters |
| `internal/provider/*` | Model catalogs, credentials, defaults and provider compatibility; [configuration](configuration.md) |
| `internal/tool` | Host tools, web tools, questions, Skills and MCP discovery/result adapters; [execution guide](execution-sessions.md#tool-execution-boundary) |
| `internal/evidence` | Leaf source/evidence contract retained as tool-result metadata; deterministic source IDs, validation, cloning |
| `internal/web` | Provider-neutral search/fetch requests and results, classified errors, domain policy, pure source resolver, deterministic model rendering |
| `internal/web/exa` | Exa Search REST adapter: wire types, bounded HTTP, error classification, normalization into evidence |
| `internal/web/httpfetch` | Page fetcher: URL policy, IP-literal checks, standard-transport proxy/DNS/dialing, bounded redirects and bodies, HTML-to-Markdown extraction |
| `internal/guard` | File/command/network policies, identity-bound MCP permissions, transient grants and allow/ask/deny decisions |
| `internal/session` | Versioned JSONL replay, tree navigation, compaction context |
| `internal/trust` | Protected-resource discovery and global Trust decisions |
| `internal/skill` | Agent Skill discovery, SKILL.md parse, source layering, embedded builtins |
| `internal/config` | Instance-local Viper precedence, effective snapshots, and locked atomic preference/credential persistence |
| `internal/deps` | Verified ripgrep, Windows Git Bash, pinned agent-browser and Cua provisioning, including upstream browser skill resources |
| `internal/desktop` | Pinned Cua admission, shared/owned runtime lifecycle, Run sessions, serialized calls, mode/image limits and setup validation; [platform scope](desktop.md) |
| `internal/browser` | Process-owned browser names, environment, connection and bounded cleanup; app owns wiring, Loop remains unaware |
| `internal/update` | Checksum-validated GitHub release updates |
| `internal/hostpath` | Host path membership, tilde expansion, slash-normalized display |
| `internal/jsonutil`, `internal/apitest` | Focused shared JSON and test infrastructure |

Create a package only when implementation requires it. Avoid vague packages
such as `core`, `types`, `services`, `utils`, or `helpers`.

## Dependency and ownership rules

- `cmd/aice` delegates assembly to `app`; CLI and TUI use application capabilities.
  Dependencies are constructor-wired, with no mutable service registry, service
  locator, `init()` wiring or DI framework. Fixed private dispatch tables are data.
- Interfaces belong to consumers. Constructors normally return concrete types;
  factories may return the consumer capability they select. Create a package only
  for a distinct responsibility, not to make a diagram symmetric.
- `agent` does not import UI, concrete tools, Session storage, provider SDKs or
  `guard`. Non-empty tools and dynamic catalogs require an injected Guard.
  The recorder persists accepted source messages before dependent effects.
- `llm` owns canonical messages, usage and thinking semantics. Providers own model
  catalogs, capabilities and credentials; `api` owns SDK/wire translation.
  Provider packages do not import `agent`. App selects Session routing identity;
  providers encode it on the transport.
- Sessions are the sole durable transcript. Context, history search and display
  are derived views. Do not merge states with different publication or invalidation
  boundaries merely because their fields look similar.
- Host tools inherit process privileges. Guard checks calls; Trust gates project
  inputs. Neither is an OS sandbox. See [execution boundary](execution-sessions.md#tool-execution-boundary)
  and [Project Trust](project-trust.md).
- Prefer the standard library. New direct dependencies require a concrete reason,
  maintenance/license review and explicit user approval. Versions live in
  [go.mod](../go.mod), not duplicated catalogs in these guides.

Selected non-standard dependencies and their reason:

| Dependency | Reason / license |
| --- | --- |
| `mvdan.cc/sh/v3` | Bash AST for dangerous-command checks; MIT |
| `gopkg.in/yaml.v3` | Agent Skill frontmatter; MIT |
| `golang.org/x/text` | Unicode normalization for read-path matching; BSD-3-Clause |
| `golang.org/x/image` | BMP/WebP decoding; BSD-3-Clause |
| `golang.org/x/net` | HTML parsing, charset and IDNA; BSD-3-Clause |
| Goldmark | Markdown structure, including nested/streaming code blocks; MIT |
| Go MCP SDK | Protocol transport behind `mcpclient`; Apache-2.0/MIT transition, see Cua provenance below |

Imported resources retain source/commit and license notices. Helper provenance
is owned by [agent-browser VENDOR.md](../internal/deps/agentbrowser/VENDOR.md)
and [Cua VENDOR.md](../internal/deps/cua/VENDOR.md). AICE remains Apache-2.0.

## Skills

`skill` scans and parses built-in, user and trusted-project resources through
one path. App supplies roots, resolves same-name priority (project > user >
builtin), and injects only names/descriptions into the initial prompt.
The `skill` tool loads the parsed body on demand; explicit slash selection is
Guard-checked by app before input admission and retained in the user message.
TUI never reads Skill files. Host Skill directories receive read-only resource
access through Guard, without bypassing sensitive-file rules.

Formats, limits and user paths live in [Agent Skills](configuration.md#agent-skills).
Versioned browser/Cua reference material belongs to dependency provisioning;
it is not a second independently scanned catalog. No hot reload or Loop
middleware is implemented.

## Planned extensions and restraint

Plan mode, subagents and memory remain unimplemented product scope. `/btw` is a
tool-free side conversation, not a subagent executor. Model-native web search is
only a reserved priority entry; Exa is the only production search adapter and
there is no execution-error failover. See [Web](web.md).

A concrete requirement must justify extensions to Loop semantics, message/Session
types, permissions or state ownership. Use existing boundaries first. Plan mode
would need Guard enforcement; subagents would need explicit child cancellation,
permission and usage ownership; memory would need retention and retrieval rules.
No additional framework, plugin bus, public SDK, GUI/Web frontend, RPC/ACP layer,
second transcript store or sandbox manager is implied by the current design.
