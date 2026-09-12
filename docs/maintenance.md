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
| Permission checks and replies | [guard_bridge.go](../internal/app/guard_bridge.go), [guard.go](../internal/guard/guard.go), [command.go](../internal/guard/command.go) | [app_guard_test.go](../internal/app/app_guard_test.go), [guard_test.go](../internal/guard/guard_test.go) |
| Trust, prompts, Skills | [project_trust.go](../internal/app/project_trust.go), [project_prompt.go](../internal/app/project_prompt.go), [skills.go](../internal/app/skills.go), [skill/discover.go](../internal/skill/discover.go) | [project_prompt_test.go](../internal/app/project_prompt_test.go), [skills_test.go](../internal/app/skills_test.go), [resource_test.go](../internal/trust/resource_test.go) |
| Interactive commands and `/new` | [interactive_commands.go](../internal/app/interactive_commands.go), [tui/command.go](../internal/tui/command.go) | [interactive_commands_test.go](../internal/app/interactive_commands_test.go), [command_test.go](../internal/tui/command_test.go) |
| Transcript folds and mouse input | [tui/fold.go](../internal/tui/fold.go), [tui/fold_mouse.go](../internal/tui/fold_mouse.go), [tui/transcript_viewport.go](../internal/tui/transcript_viewport.go), [app/display_output.go](../internal/app/display_output.go) | [fold_test.go](../internal/tui/fold_test.go), [fold_mouse_test.go](../internal/tui/fold_mouse_test.go), [display_output_test.go](../internal/app/display_output_test.go) |
| Side conversations | [app/side_thread.go](../internal/app/side_thread.go), [tui/side_thread.go](../internal/tui/side_thread.go) | [side_thread_lifecycle_test.go](../internal/app/side_thread_lifecycle_test.go), [tui/side_thread_test.go](../internal/tui/side_thread_test.go) |
| Provider and protocol changes | [providers.go](../internal/app/providers.go), [provider](../internal/provider), [api](../internal/api) | The affected provider and protocol adapter tests; shared fixtures in [apitest](../internal/apitest) |
| Browser lifecycle | [browser](../internal/browser), [app/browser.go](../internal/app/browser.go), [deps/agentbrowser.go](../internal/deps/agentbrowser.go) | [native_test.go](../internal/browser/native_test.go), [app/browser_test.go](../internal/app/browser_test.go) |
| Print integrations | [json_printer.go](../internal/app/json_printer.go), [Harbor adapter](../integrations/harbor/aice_agent.py) | [stream_printer_test.go](../internal/app/stream_printer_test.go), [Harbor guide](../integrations/harbor/README.md) |

For a new feature, identify what state it adds, who owns that state, how it
ends, and whether it is durable. Prefer an ordinary tool, application command,
provider adapter, or UI change when that boundary is sufficient. Changes to
Loop control flow or persistence need a specific reason and boundary tests;
adding a framework is not a substitute for identifying the owner.

### Follow one interactive request

`newRunEnvironment` assembles the workspace, startup instructions, tools and
Guard. `Interactive` owns their process lifetime and closes the conversation's
current Store after the frontend stops. A model change rebuilds the Loop while
reusing the Session's Guard; `/new` detaches the Store and clears its grants.

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

## Known discrepancies

### Browser acceptance gaps

The [browser acceptance matrix](browser.md#maintenance-and-verification) records
native macOS/Linux coverage separately from offline fixtures. Full real-model
browsing/vision and print, inspect auto-detect with Chrome Allow and login state,
kill/restart, simultaneous external connections, tab/browser loss, and Linux
external-browser/TUI acceptance remain unverified. Complete these cases on an
isolated user-approved profile before claiming the entire matrix passes. Release
bytes match npm for all supported assets, but a second independent network check
remains outstanding. Windows browser support is deliberately disabled pending
native lifecycle/Job Object validation: the daemon must survive completion of
the launching bash command before Windows support can be enabled.

Upstream 0.37.1 cancellation stops the CLI while an already queued browser wait
can delay later commands. Acceptance must allow eventual recovery and require a
fresh observation; it must not claim immediate action cancellation or rollback.
