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
| Durable history and compaction | [app/session.go](../internal/app/session.go), [app/compact.go](../internal/app/compact.go), [session/store.go](../internal/session/store.go), [session/replay.go](../internal/session/replay.go) | [store_test.go](../internal/session/store_test.go), [app/compact_test.go](../internal/app/compact_test.go) |
| Permission checks and replies | [guard_bridge.go](../internal/app/guard_bridge.go), [guard.go](../internal/guard/guard.go), [command.go](../internal/guard/command.go) | [app_guard_test.go](../internal/app/app_guard_test.go), [guard_test.go](../internal/guard/guard_test.go) |
| Trust, prompts, Skills | [project_trust.go](../internal/app/project_trust.go), [project_prompt.go](../internal/app/project_prompt.go), [skills.go](../internal/app/skills.go), [skill/discover.go](../internal/skill/discover.go) | [project_prompt_test.go](../internal/app/project_prompt_test.go), [skills_test.go](../internal/app/skills_test.go), [resource_test.go](../internal/trust/resource_test.go) |
| Interactive commands and `/new` | [interactive_commands.go](../internal/app/interactive_commands.go), [tui/command.go](../internal/tui/command.go) | [interactive_commands_test.go](../internal/app/interactive_commands_test.go), [command_test.go](../internal/tui/command_test.go) |
| Side conversations | [app/side_thread.go](../internal/app/side_thread.go), [tui/side_thread.go](../internal/tui/side_thread.go) | [side_thread_lifecycle_test.go](../internal/app/side_thread_lifecycle_test.go), [tui/side_thread_test.go](../internal/tui/side_thread_test.go) |
| Provider and protocol changes | [providers.go](../internal/app/providers.go), [provider](../internal/provider), [api](../internal/api) | The affected provider and protocol adapter tests; shared fixtures in [apitest](../internal/apitest) |
| Print integrations | [json_printer.go](../internal/app/json_printer.go), [Harbor adapter](../integrations/harbor/aice_agent.py) | [stream_printer_test.go](../internal/app/stream_printer_test.go), [Harbor guide](../integrations/harbor/README.md) |

For a new feature, identify what state it adds, who owns that state, how it
ends, and whether it is durable. Prefer an ordinary tool, application command,
provider adapter, or UI change when that boundary is sufficient. Changes to
Loop control flow or persistence need a specific reason and boundary tests;
adding a framework is not a substitute for identifying the owner.

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

## Known discrepancies

These findings were checked against `2109d2e` during the documentation review
on 2026-09-05. They are unresolved implementation work, not newly approved
behavior. This was a review of documentation and selected runtime boundaries,
not an exhaustive correctness, security, or provider compatibility audit.
Recheck an entry before changing its code.

### Guard approval behavior

**Grant lifetime — product decision confirmed, implementation pending.**
The intended Session lifetime is defined in
[Execution](execution-sessions.md#default-wired-behavior). Currently
`newRunEnvironment` creates one Guard, `Interactive` retains it on
`interactiveSession`, and `rebuildAgentLoop` reuses it. `beginMainRun`,
`endMainRun`, and `slashNew` do not clear grants. The menu says “for this run”
although grants survive both later Agent runs and `/new`.

Acceptance: grants survive Agent-run and provider/credential changes within a
Session; `/new` clears all dynamic grants while preserving configured default
policies, skill read roots, and the invocation's `--yolo` setting. Resuming a
Session in another process must not restore grants from disk. Update menu
labels and tests to use the same Session scope.

**Exact-command grant — confirmed implementation mismatch.**
`applyGuardAskGrant` calls `AllowCommandSession`, which adds the command to
`allowedCmdPatterns`. `compileCommandPattern` uses `strings.Contains` for this
entry. Authorizing `rm -rf ./scratch` therefore also lets
`rm -rf ./scratch-other` pass the dangerous-command check when no other rule
blocks it. This is broader than the menu's “exact command” choice.

Reproduced using only `Guard.Check` and `AllowCommandSession`; no shell command
was executed. Acceptance: exact grants match the whole command string;
changed arguments and compound commands must be evaluated independently.
Keep deliberate configured patterns separate from exact interactive grants,
and preserve auto-deny precedence.

**Early ask skips later policies — confirmed implementation mismatch.**
`Guard.Check` returns immediately for a dangerous command, before checking file
policies and all extracted paths. With an existence probe reporting a protected
`.env`, `cat .env` returns `deny`, but `sudo cat .env` returns `ask`. The app's
`--yolo` adapter promotes that ask, and the loop also proceeds directly after
an interactive allow reply; neither completes the remaining checks.

Reproduced at the decision boundary with a fake existence probe; no real secret
file was read and no command was executed. Acceptance: a matching hard denial
wins even when another rule asks, including multiple paths in one call.
Interactive approval and `--yolo` must not bypass later policies. Test the
combined rules through both Guard and the app/loop bridge, not only isolated
matchers.

### Startup state and `/new`

**Skill refresh — confirmed wording mismatch.** `slashNew` resets history,
store, and usage but retains the startup catalog, tools, and prompt. The
`skillsScanReminder` in [skills.go](../internal/app/skills.go) still says that
starting a new Session picks up installed/removed skills. The user guide now
specifies restart, matching the current implementation.

Acceptance: align the reminder with restart-only discovery. If `/new` is later
meant to refresh startup inputs, decide how Trust, prompts, skill bodies, and
Guard read roots refresh together; do not rescan only the menu.

**Unsaved `/trust` choice — confirmed no-op; desired UX unresolved.**
`slashTrust` persists `choice.Updates` when non-empty. Otherwise it returns
`trustResultMessage` without updating the effective Trust state or prompt.
The success text claims a Session-only choice was applied and requests a
restart, but an unsaved choice cannot survive that restart.

Acceptance requires deciding whether to remove those choices from `/trust`,
apply them at an explicit environment-refresh boundary, or introduce another
well-defined behavior. Do not silently hot-reload trusted inputs or save a
choice presented as temporary. Test both the reported and effective state.
