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

## Settings and Usage verification

Entry points are `config/settings_schema.go`, `sources.go`, `settings_patch.go`,
`app/settings.go`, `settings_apply.go`, `settings_lifecycle.go`, `usage.go`, and
`tui/settings_panel.go`, `settings_action.go`, `settings_collection.go`,
`usage_panel.go`. Config owns storage/source semantics, app owns resource
publication, and TUI owns only navigation and drafts. To add a scalar preference,
update its config definition/parser, app description/timing, and actual behavior;
existing value kinds require no additional TUI key routing. Domain operations
remain in their existing modules.

| Evidence | Coverage |
| --- | --- |
| Config/application tests | Typed zero/false/empty/unset, frozen sources, peer writes, damaged files, prepare/save failures, stale runs, main/BTW edit exclusion, cost completeness and non-consuming reads |
| Modal tests | 80×24, 120×40, wide and tiny layouts; CJK/emoji draft and paste isolation; mouse enum save, array drafts, permission preemption, stale reads and save completion after close |
| Actual CLI with Bubble Tea, isolated configuration and fake model | Settings navigation/search, duration save including nanoseconds, Usage and empty Session views |
| Native desktop IME candidate window and physical mouse | Not exercised: Computer Use denied access to macOS Terminal; terminal-cell cursor tests and synthetic events do not establish native acceptance |
| Native Linux/Windows terminal behavior | Not exercised on this macOS host; CI/portable tests do not replace it |
| Live OAuth and paid provider/search calls through Settings | Not exercised; existing domain tests use synthetic credentials and local fake services |

## Known discrepancies

### Self-update OpenPGP dependency warning

`govulncheck ./...` reports [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)
because `github.com/creativeprojects/go-selfupdate v1.6.0` imports the
unmaintained `golang.org/x/crypto/openpgp` package. There is no patched version
listed for that package. AICE's [`newClient`](../internal/update/update.go)
configures only `ChecksumValidator` (SHA-256); it does not configure a
`PGPValidator` or parse PGP keys or signatures. The scanner still reports the
package initialization and shared error types, so the scan is not clean.

Keep the finding visible until an upstream release removes or replaces this
dependency, or a separately reviewed updater change does so. Preserve checksum
rejection and executable replacement tests; do not disable validation or
suppress the finding to make the scan pass.

### Composer click positioning and textarea capabilities

Composer mouse input does not position the editing caret. The composer keeps
the real terminal caret from `textarea.Cursor()` as the IME candidate-window
anchor. See [composer mouse input](../internal/tui/composer_mouse.go),
[file references](../internal/tui/composer_files.go), and
[paste tokens](../internal/tui/composer_paste.go).

The pinned `charm.land/bubbles/v2 v2.2.1` textarea provides `PositionAt(x, y)`
for read-only visible-cell mapping, and `BeginSelection` / `EndSelection` can
move the cursor without replacing text. `Line()`, `Column()` (a rune index),
`LineInfo()`, `Cursor()`, and `ScrollYOffset()` expose its current position.
The public coordinate APIs remove the need to simulate cursor navigation for
hit testing, but do not yet establish reliable general mouse positioning:

- `PositionAt` measures individual runes, not whole grapheme clusters. In
  `👨‍👩‍👧‍👦cd`, column 2 maps to rune 2 inside the emoji instead of rune 7
  before `c`; `👍🏽cd` similarly maps column 2 inside the skin-tone sequence.

- Wrapping can split a grapheme: at width 40, 38 ASCII characters followed by
  `👨‍👩‍👧‍👦cd` place `👨` on the first row and start the second row with a ZWJ.
  Combining accents and emoji skin-tone modifiers can also split across rows.
- Repeated `CursorDown()` is not a dependable visible-row iterator. At width 6,
  `中文测试甲乙\nlast` can stop advancing at the second wrapped Chinese row,
  before the synthetic trailing row and the next logical line.
- A shallow `textarea.Model` copy shares its internal viewport. Moving a copied
  model to the end can scroll the original model and leave its original caret
  outside the visible area. A copied model is not an isolated geometry probe.
- `SetCursorColumn()` accepts positions within graphemes and does not itself
  reposition the viewport. `LineInfo()` only describes the current position;
  obtaining every position by repeated cursor movement would add navigation
  workarounds and layout traversal to AICE.

These are dependency limitations to reassess on upgrade, not behavior to lock
in with regression assertions. From the repository root, save this diagnostic
as `/tmp/aice-textarea-capabilities.go` and run
`GOPROXY=off GOSUMDB=off go run /tmp/aice-textarea-capabilities.go`. It uses the
locally cached pinned dependencies and prints observations without requiring
the defects to remain present:

```go
package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"github.com/charmbracelet/x/ansi"
)

func input(value string, width, height int) textarea.Model {
	m := textarea.New()
	m.Prompt, m.ShowLineNumbers, m.CharLimit = "", false, 0
	m.MaxHeight = 100
	m.SetWidth(width)
	m.SetHeight(height)
	m.SetVirtualCursor(false)
	m.Focus()
	m.SetValue(value)
	m.MoveToBegin()
	m, _ = m.Update(nil)
	return m
}

func main() {
	for _, text := range []string{"👨‍👩‍👧‍👦cd", "👍🏽cd"} {
		m := input(text, 40, 3)
		fmt.Printf("hit after emoji %q: %+v\n", text, m.PositionAt(2, 0))
	}
	m := input(strings.Repeat("a", 38)+"👨‍👩‍👧‍👦cd", 40, 3)
	fmt.Printf("wrapped grapheme: %q\n", ansi.Strip(m.View()))
	m = input("中文测试甲乙\nlast", 6, 6)
	for range 5 {
		fmt.Printf("down: row=%d column=%d screen=%v\n",
			m.Line(), m.Column(), m.Cursor().Position)
		m.CursorDown()
	}
	m = input(strings.Repeat("line\n", 8)+"last", 10, 2)
	probe := m
	probe.MoveToEnd()
	fmt.Printf("original after copy moved: scroll=%d caret=%v\n",
		m.ScrollYOffset(), m.Cursor().Position)
}
```

Click positioning remains deferred; no dependency fork or replacement editor
is maintained. Upstream hit testing and wrapping must agree on whole grapheme
boundaries, and cursor movement must maintain the viewport. AICE would still
own snapping hits on confirmed file references and paste tokens to their
atomic boundaries. Calling `composerInput.SetValue()` to move the caret would
clear file-reference spans; cursor-only changes must preserve the draft and
attachment identities. [`composer_file_view.go`](../internal/tui/composer_file_view.go)
uses `PositionAt(0, y)` only to locate visible row starts, then measures whole
segments for file-label styling. This avoids the horizontal per-rune hit-test
defect and does not require a second textarea or simulated cursor movement.

Acceptance requires visible-position tests for soft wrapping, scrolling,
trailing spaces, CJK, combining sequences and emoji, plus file/paste-token
integrity and real-caret/IME alignment. Passing single-line ASCII cases is
insufficient to claim composer click positioning.

### Web search acceptance gaps

The web tools were verified with offline fixtures, injected HTTP
transports/clients
and the application-level fake backend. Not yet verified: a real Exa request
(the opt-in test in [Verification](collaboration.md#web-checks) has not been run
against a live key), a real public page through `web_fetch` on the open
internet, and the `/web` menu in a real terminal beyond the Bubble Tea unit
tests. Windows and Linux runs of the new tests are unverified locally. Record
results here or in the owning guide when these are exercised; do not claim
end-to-end Exa acceptance until then.

### Browser acceptance gaps

The [browser acceptance record](browser.md#maintenance-and-verification) owns
platform coverage, outstanding cases, and acceptance conditions. Offline fixtures
and native macOS/Linux helper tests do not establish full TUI or actual-model
acceptance. That acceptance remains incomplete, and Windows browser support
remains disabled pending native lifecycle validation. Consult the record before
claiming support or extending browser lifecycle behavior.
