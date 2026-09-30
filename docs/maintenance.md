# Maintaining AICE

Start with [AGENTS.md](../AGENTS.md) for task routing and
[Architecture](architecture.md#design-philosophy) for the design philosophy.
This guide connects those rules to code and records unresolved discrepancies;
it does not define a second set of product behavior.

## Trace a change

Choose the affected path, then read its implementation, callers, and relevant
tests. Search for the named symbols rather than relying on line numbers.

| Path | Entry points | Existing verification starting points |
| --- | --- | --- |
| Startup and composition | [app.go](../internal/app/app.go): `NewCommand`, `newRunEnvironment`, `Interactive`, `Print` | [app_test.go](../internal/app/app_test.go), [app_tools_test.go](../internal/app/app_tools_test.go) |
| Model/tool loop | [loop.go](../internal/agent/loop.go), [tools.go](../internal/agent/tools.go), [finalize.go](../internal/agent/finalize.go) | [loop_test.go](../internal/agent/loop_test.go), [retry_test.go](../internal/agent/retry_test.go) |
| Steering and follow-up | [interactive_run.go](../internal/app/interactive_run.go), [mailbox.go](../internal/interaction/mailbox.go) | [interactive_run_test.go](../internal/app/interactive_run_test.go), [mailbox_test.go](../internal/interaction/mailbox_test.go) |
| Durable history and compaction | [app/conversation.go](../internal/app/conversation.go), [app/session.go](../internal/app/session.go), [app/compact.go](../internal/app/compact.go), [session/store.go](../internal/session/store.go), [session/replay.go](../internal/session/replay.go) | [store_test.go](../internal/session/store_test.go), [app/compact_test.go](../internal/app/compact_test.go), [long_task_test.go](../internal/app/long_task_test.go) |
| Project history picker and restoration | [app/session_browser.go](../internal/app/session_browser.go), [app/session_resume.go](../internal/app/session_resume.go), [app/session_transcript.go](../internal/app/session_transcript.go), [tui/session_picker.go](../internal/tui/session_picker.go), [session/read.go](../internal/session/read.go) | [app/session_browser_test.go](../internal/app/session_browser_test.go), [app/session_transcript_test.go](../internal/app/session_transcript_test.go), [tui/session_picker_test.go](../internal/tui/session_picker_test.go), [session/read_test.go](../internal/session/read_test.go) |
| Permission checks and replies | [guard_bridge.go](../internal/app/guard_bridge.go), [guard.go](../internal/guard/guard.go), [command.go](../internal/guard/command.go) | [app_guard_test.go](../internal/app/app_guard_test.go), [guard_test.go](../internal/guard/guard_test.go) |
| Trust, prompts, Skills | [project_trust.go](../internal/app/project_trust.go), [project_prompt.go](../internal/app/project_prompt.go), [skills.go](../internal/app/skills.go), [skill/discover.go](../internal/skill/discover.go) | [project_prompt_test.go](../internal/app/project_prompt_test.go), [skills_test.go](../internal/app/skills_test.go), [resource_test.go](../internal/trust/resource_test.go) |
| Interactive commands and `/new` | [interactive_commands.go](../internal/app/interactive_commands.go), [tui/command.go](../internal/tui/command.go) | [interactive_commands_test.go](../internal/app/interactive_commands_test.go), [command_test.go](../internal/tui/command_test.go) |
| Transcript folds and mouse input | [tui/fold.go](../internal/tui/fold.go), [tui/fold_mouse.go](../internal/tui/fold_mouse.go), [tui/transcript_viewport.go](../internal/tui/transcript_viewport.go), [app/display_output.go](../internal/app/display_output.go) | [fold_test.go](../internal/tui/fold_test.go), [fold_mouse_test.go](../internal/tui/fold_mouse_test.go), [display_output_test.go](../internal/app/display_output_test.go) |
| Side conversations | [app/side_thread.go](../internal/app/side_thread.go), [tui/side_thread.go](../internal/tui/side_thread.go) | [side_thread_lifecycle_test.go](../internal/app/side_thread_lifecycle_test.go), [tui/side_thread_test.go](../internal/tui/side_thread_test.go) |
| Provider and protocol changes | [providers.go](../internal/app/providers.go), [provider](../internal/provider), [api](../internal/api) | The affected provider and protocol adapter tests; shared fixtures in [apitest](../internal/apitest) |
| Browser lifecycle | [browser](../internal/browser), [app/browser.go](../internal/app/browser.go), [deps/agentbrowser.go](../internal/deps/agentbrowser.go) | [native_test.go](../internal/browser/native_test.go), [app/browser_test.go](../internal/app/browser_test.go) |
| Web search and fetch | [app/web.go](../internal/app/web.go) (`bindWeb`), [app/web_commands.go](../internal/app/web_commands.go), [config/web.go](../internal/config/web.go), [web/resolve.go](../internal/web/resolve.go), [web/exa/client.go](../internal/web/exa/client.go), [web/httpfetch/fetch.go](../internal/web/httpfetch/fetch.go), [guard/network.go](../internal/guard/network.go), [tool/web_search.go](../internal/tool/web_search.go), [tool/web_fetch.go](../internal/tool/web_fetch.go) | [app/web_test.go](../internal/app/web_test.go), [app/web_commands_test.go](../internal/app/web_commands_test.go), [config/web_test.go](../internal/config/web_test.go), [web/web_test.go](../internal/web/web_test.go), [exa_test.go](../internal/web/exa/exa_test.go), [fetch_test.go](../internal/web/httpfetch/fetch_test.go), [guard/network_test.go](../internal/guard/network_test.go) |
| MCP transport and run catalog | [mcpclient/client.go](../internal/mcpclient/client.go), [app/mcp_owner.go](../internal/app/mcp_owner.go), [app/mcp_catalog.go](../internal/app/mcp_catalog.go), [agent/tool_selection.go](../internal/agent/tool_selection.go) | [mcpclient/lifecycle_test.go](../internal/mcpclient/lifecycle_test.go), [app/mcp_startup_test.go](../internal/app/mcp_startup_test.go), [app/mcp_loop_test.go](../internal/app/mcp_loop_test.go), [agent/tool_selection_test.go](../internal/agent/tool_selection_test.go) |
| MCP management, authorization and OAuth | [app/mcp_management.go](../internal/app/mcp_management.go), [app/mcp_guard.go](../internal/app/mcp_guard.go), [config/mcp_permissions.go](../internal/config/mcp_permissions.go), [mcpauth](../internal/mcpauth) | [app/mcp_tui_test.go](../internal/app/mcp_tui_test.go), [app/mcp_permissions_test.go](../internal/app/mcp_permissions_test.go), [app/mcp_oauth_login_test.go](../internal/app/mcp_oauth_login_test.go), [app/mcp_oauth_refresh_test.go](../internal/app/mcp_oauth_refresh_test.go) |
| Managed Computer Use and MCP evaluation | [app/mcp_desktop.go](../internal/app/mcp_desktop.go), [desktop/mcp.go](../internal/desktop/mcp.go), [lexical retrieval tests](../internal/app/mcp_search_test.go) | [app/mcp_desktop_lifecycle_test.go](../internal/app/mcp_desktop_lifecycle_test.go), [app/desktop_native_linux_test.go](../internal/app/desktop_native_linux_test.go), [app/desktop_model_native_darwin_test.go](../internal/app/desktop_model_native_darwin_test.go) |
| Print integrations | [json_printer.go](../internal/app/json_printer.go), [Harbor adapter](../integrations/harbor/aice_agent.py) | [stream_printer_test.go](../internal/app/stream_printer_test.go), [Harbor guide](../integrations/harbor/README.md) |

For a new feature, identify what state it adds, who owns that state, how it
ends, and whether it is durable. Prefer an ordinary tool, application command,
provider adapter, or UI change when that boundary is sufficient. Changes to
Loop control flow or persistence need a specific reason and boundary tests;
adding a framework is not a substitute for identifying the owner.

### Follow one interactive request

`prepareRunEnvironment` assembles the workspace, startup instructions, tools
and Guard after model/Trust loading. `Interactive` owns their process lifetime
and closes the conversation's current Store after the frontend stops. Provider
changes rebuild the Loop; ordinary model selection reuses it unless recovering
an unavailable model. Both retain the Session Guard. `/new` detaches the Store
and clears transient grants.

`NewRun` creates an input mailbox and lazily starts storage. `beginMainRun`
freezes settings and registers one transcript owner. The Loop accepts inputs,
records ended assistants before tools, and records results before later effects.
`conversationState.recordMessage` serializes durable appends and publishes only
paired history; side questions copy that view under its short read lock. Model
streaming and filesystem I/O do not run while that read lock is held.

The frontend controller creates the cancellation context and owns its update
channel. Cancellation reaches the Loop, providers and tools; the message recorder
has a bounded cleanup deadline for known results. `endMainRun` removes transient
ownership, the mailbox seals, and the controller closes the update channel.
Frontend shutdown cancels and waits for its controllers before application
storage closes. See [Runtime contracts](contracts.md#concurrency-and-tui).

These boundaries provide concrete maintenance checks: an authorization lifetime
change belongs to Session/Guard wiring, Bash output changes stay in the tool,
and a Session record change reaches storage, app coordination and consumers
without requiring provider SDK changes. Avoid splitting these owners merely to
reduce file size; extract a new boundary when a real change needs it.

### Main-path acceptance

Use the following cross-boundary tests when changing the main TUI/Print path.
Their presence is a verification map, not a claim that they passed on this host.

| Boundary | Representative tests |
| --- | --- |
| Attachment admission and immutable input | [file_input_test.go](../internal/app/file_input_test.go) |
| Prepared-run revisions and settings exclusion | [settings_test.go](../internal/app/settings_test.go) |
| Steering, follow-up and terminal sealing | [mailbox_test.go](../internal/interaction/mailbox_test.go), [interactive_run_test.go](../internal/app/interactive_run_test.go) |
| Complete tool calls and Guard checks | [stream_failure_test.go](../internal/agent/stream_failure_test.go), [guard_scopes_test.go](../internal/app/guard_scopes_test.go) |
| Record-before-effect and cancellation recovery | [persistence_boundary_test.go](../internal/app/persistence_boundary_test.go), [mutation_persistence_test.go](../internal/app/mutation_persistence_test.go) |
| Long runs and compaction failures | [long_task_test.go](../internal/app/long_task_test.go) |
| Frontend shutdown and current-resource cleanup | [run_test.go](../internal/tui/run_test.go), [web_lifecycle_test.go](../internal/app/web_lifecycle_test.go), [session_test.go](../internal/app/session_test.go) |

Print has ephemeral history unless a Session is requested; interactive execution
owns durable conversation publication and concurrent side snapshots. Preserve
these differences. Lifecycle admission, transcript ownership and TUI delivery
previews also have different authority and must not be merged merely to reduce
state. GUI, remote transport and simultaneous frontend control are not implemented.
MCP/CUA acceptance remains scoped by the domain guides below.

## Resolving discrepancies

Code proves current behavior, tests prove the cases they cover, and an owning
document states the intended contract. None alone proves that a conflicting
piece is correct. Use this procedure:

1. Describe the trigger, observed behavior, intended behavior, and affected
   boundary. Read the complete call path before assigning the discrepancy.
2. Check callers, tests, and focused git history for the reason behind it.
   A comment, passing test, or newer timestamp alone is not a design decision.
3. If the implementation clearly matches an accepted change, correct stale
   documentation and comments. If the intended contract is clear and the code
   violates it, record or fix a regression with a focused test. Do not weaken
   the contract just to describe the bug as normal behavior.
4. Ask the user when the evidence leaves multiple product choices, especially
   permission lifetime, persistence, compatibility, or feature scope. Continue
   independent work while that decision is pending.
5. Update the owning document and cross-links together. For unresolved work,
   record the evidence, decision status, and acceptance conditions here; remove
   the entry once the fix and durable documentation land. Do not retain a
   growing archive of completed implementation plans.

Keep user guides about observable behavior, architecture about ownership and
tradeoffs, contracts about runtime guarantees, and local comments about intent
that cannot be understood from the code alone. Internal enum lists, private
field names, provider counts, and build commands are easy to duplicate and
forget; link to their owner when a copy adds no user value.

## Documentation upkeep

Describe the current design directly, including its rationale, ownership and
limits. Remove superseded alternatives, transition instructions, completed plans
and dated verification narratives when the final design is in place. Git history
retains implementation history; maintained guides are not a change journal.
Keep reproducible verification commands and evaluation task specifications.
Session recovery and supported-format rejection are current behavior, not
historical documentation.

## Settings and Usage verification

Config owns typed fields, frozen sources and partial writes; app owns resource
publication; TUI owns navigation and drafts. Start at `config/settings_schema.go`,
`app/settings_apply.go`, `app/settings_lifecycle.go` and `tui/settings_panel.go`.
Tests cover stale requests, partial commits, main/BTW exclusion, modal layouts,
attachments and the actual CLI/Bubble Tea flow. Native IME/physical mouse,
Linux/Windows desktop behavior and live provider/OAuth calls require separate
acceptance; synthetic terminal events do not establish those results.

## Known discrepancies

### MCP verification limits

[MCP verification](mcp.md#verification-evidence) owns service and platform scope.
Deterministic connection/discovery/permission tests do not establish broad model
task quality. Search is lexical and does not translate languages; models must
retain original-language keywords or browse/refine. Cross-process settings
changes do not immediately revoke a frozen Run because there is no watcher.
Remote close errors do not prove remote session deletion.

### Computer Use integration

The current [managed boundary](desktop.md#managed-mcp-boundary) delegates native
target, token, capture and input semantics to Cua. AICE retains verified runtime
admission, Run sessions, mode/image limits, Guard and generic result retention.
Old typed-wrapper acceptance does not validate this forwarding path.

[Platform evidence](desktop.md#platform-evidence) owns outstanding macOS input,
Linux Unicode/launch/focus, Windows action admission and multi-display cursor
limitations. Current-pin real-model task completion, screenshot coordinates,
degraded observation, foreground continuation and physical input coexistence
need native evidence. Tests or compile success must not erase failed or
unverified postconditions. Reproduce through the [native gates](collaboration.md#computer-use-checks)
and [real-model gate](collaboration.md#explicit-real-model-desktop-gate).

### Self-update OpenPGP dependency warning

A prior `govulncheck ./...` run reported [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)
because `github.com/creativeprojects/go-selfupdate v1.6.0` imports the
unmaintained `golang.org/x/crypto/openpgp` package. The pinned dependency still
imports it. AICE's [`newClient`](../internal/update/update.go) configures only `ChecksumValidator` (SHA-256); it does not configure a
`PGPValidator` or parse PGP keys or signatures. The prior scan included package
initialization and shared error types; this audit did not rerun the scanner
or establish exploitability.

Keep the finding visible until an upstream release removes or replaces this
dependency, or a separately reviewed updater change does so. Preserve checksum
rejection and executable replacement tests; do not disable validation or
suppress the finding to make the scan pass.

### Settings shortcut hint without a binding

The Settings command description in [interactive_commands.go](../internal/app/interactive_commands.go)
advertises `Ctrl+,`, but [keymap.go](../internal/tui/keymap.go),
[input_actions.go](../internal/tui/input_actions.go) and
[input_route.go](../internal/tui/input_route.go) provide no Settings binding.
[submit.go](../internal/tui/submit.go) opens Settings through slash navigation.
The user guide therefore documents `/settings` only. The code hint remains
incorrect: either remove it or implement the shortcut with a routing-conflict
check and a focused input test.

### Composer click positioning and textarea capabilities

Composer clicks do not position the editing caret. The real terminal caret from
`textarea.Cursor()` remains the IME anchor. [Composer mouse handling](../internal/tui/composer_mouse.go)
and [file-label rendering](../internal/tui/composer_file_view.go) are the owners.

The pinned Bubbles textarea exposes coordinate APIs, but prior diagnostics found
per-rune hit testing and wrapping that split emoji/combining graphemes, incomplete
wrapped-CJK cursor traversal, and viewport sharing after shallow copies.
Reassess these dependency limits on upgrade; do not codify them as desired
regressions. File styling uses `PositionAt(0, y)` for row starts and measures whole
segments, avoiding horizontal per-rune hit testing.

Any future click positioning must preserve file/paste attachment identities and
verify wrapping, scrolling, trailing spaces, CJK, combining sequences, emoji and
real-caret/IME alignment. `SetValue` clears file-reference spans and is not a
cursor-only operation. No dependency fork or replacement editor is maintained.

### Exa HTTP loopback validation

**Confirmed code discrepancy:** HTTP is intended only for loopback development
endpoints, but both [config validation](../internal/config/web.go) and
[adapter validation](../internal/web/exa/config.go) classify any hostname starting
with `127.` as loopback. An offline call to `WebSettings.Validate` and
`exa.ResolveBaseURL` accepts `http://127.example.com`, while rejecting
`http://gateway.example`. No credentials or network are needed to reproduce it.

If a user configures and selects such an endpoint, [Search](../internal/web/exa/client.go)
sends its `x-api-key` over HTTP to that hostname; the prefix does not establish a
loopback destination. This is a validation defect, not supported remote-HTTP
behavior. The documentation cleanup leaves code unchanged. A fix should parse
IP literals and test actual loopback membership in both validators, retaining
the deliberate `localhost` exception; add rejection cases for `127.*` hostnames
alongside existing real IPv4/IPv6 loopback and HTTPS/path-prefix cases.

The separate `web_fetch` use of standard proxy/DNS transport is intentional:
commit `c748cc41` replaced pinned direct dialing, and `6390df7` removed extra Web
approval. Its literal-address policy does not block hostnames resolving to
private addresses. See [fetch limits](web.md#web_fetch-limits); do not restore the
superseded plan's stronger SSRF guarantee as a description of current code.

### Web search acceptance gaps

Existing evidence covers offline HTTP fixtures and the app fake backend.
Live Exa, public-page fetch and native `/web` terminal acceptance remain
unrecorded; the [opt-in Exa test](collaboration.md#web-checks) is the live-service
entry. Do not infer live-service or cross-platform acceptance from local tests.

### Browser acceptance gaps

The [browser acceptance record](browser.md#maintenance-and-verification) owns
platform coverage, outstanding cases, and acceptance conditions. Offline fixtures
and native macOS/Linux helper tests do not establish full TUI or actual-model
acceptance. That acceptance remains incomplete, and Windows browser support
remains disabled pending native lifecycle validation. Consult the record before
claiming support or extending browser lifecycle behavior.
