---
name: computer-use
description: Operate native desktop apps through AICE's managed:cua MCP service with Cua Driver 0.30.4. Use when that managed service is available for a desktop task.
---

# Computer Use in AICE

This guide applies to AICE's managed Cua Driver **0.30.4** service. Use the
selected tool's pinned platform schema as the argument contract. AICE manages
the native session, connection and configured capabilities; Cua manages targets,
snapshots, element references, capture coordinates and input semantics. Loading
this guide creates no connection or permission. Stay within the user's task and
existing authorization.

## Find the available operations

Use `tool_search` with `service: "managed:cua"` and the operation you need,
for example `{"service":"managed:cua","query":"list_windows","limit":1}`.
Call the returned model-facing tool name with its complete schema on the next
model round, using its exact name without adding a namespace such as `default.`.
Raw names such as `click` identify operations; do not guess the hashed name.
Selected tools remain callable across rounds while present in the request's
tool definitions. Call them directly; do not precede every observation or action
with another search. Search selects at most five definitions at a time, not
five single-use calls. Load the small set needed for the next steps, and search
again only for a missing definition or explicit connection/catalog expiry.
Search does not grant execution permission or refresh an AX snapshot.

The service exposes `list_apps`, `list_windows`, `get_window_state`,
`launch_app`, `click`, `drag`, `type_text`, `set_value`, `press_key`, `hotkey`
and `scroll`. Public `session` is host-owned and omitted from these schemas;
do not supply or invent it. Other advertised arguments retain their upstream
meaning. Lifecycle, configuration and OS permission operations belong to AICE.

If the service is absent or unavailable, report the supplied diagnostic.
Enablement, installation, OS grants and control mode belong to Settings →
Computer Use. Do not start another Driver or switch to a separate automation
route to work around a refusal. `mcp_server_info` can show the admitted service's
initialization information; it does not itself enable or repair the service.

## Discover, observe, act, verify

1. Use `list_windows` to locate the intended application and window. A PID filter
   can reduce irrelevant results. Discover an app with `list_apps` when needed,
   and copy its actual launch identity rather than guessing a bundle ID or path.
   After launching once, inspect its windows; absence of an immediate window is
   not a reason to repeat launch.
2. Call `get_window_state` for the intended target. For a known semantic control,
   use `query` for its label and `include_screenshot:false`; this preserves room
   for actionable AX data instead of another image and the entire menu tree.
   Use a screenshot when locating an unfamiliar layout or a visual-only target.
   Inspect `elements_complete`, truncation and degraded-state details. A timed-out
   tree is partial, not evidence that the control is absent: increase `timeout_ms`
   (for example to 5000) or bound the walk with `max_depth` / `max_elements`.
   `query` filters the output; it does not guarantee a cheaper AX walk.
   A text-only model must always pass `include_screenshot:false`.
3. Perform an action grounded in that state. Copy the returned `element_token`,
   or use the advertised element-index form together with its `snapshot_id`.
   With an index, also supply the exact `window_id`; a PID can own multiple
   windows even when only one is visible. An index is not a token. Cua validates
   references; do not construct tokens or assume old references remain valid
   after a UI change or new session.
4. Read current state again and verify the business postcondition: field value,
   navigation, saved entry or submission result. `returned`, a successful RPC,
   or `effect:unverifiable` alone does not prove the task succeeded.

AICE does not automatically observe after an action, consume each observation,
or keep another window allowlist. Nevertheless, refresh state between dependent
changes and after navigation, scrolling or uncertain input. A new user message
may start a new native session; read current state before continuing a task.
Cancellation, reconnection and Session resume do not renew old references.

## Read complete results

Large application lists and observations may have a bounded initial model view.
When required data is missing, use `tool_result_read` with the exact selector
provided by the result notice, `section:"structured"` for structured JSON, and
its paging arguments. Do not invent a call ID. Read enough pages to identify the
needed control and its snapshot/token; do not mistake a clipped JSON prefix for
the complete result. Reading retained source does not refresh native state.

Native text and structured results are complementary. An empty accessibility
tree can accompany a valid image and an explanation such as an unresolved AX
window. Preserve the Driver's degraded-state and escalation guidance rather
than treating it as a disconnected MCP server. If the image or native state
cannot ground the intended action, obtain a fresh observation or report the
limitation.

## Input and coordinates

Use only fields in the selected schema. Choose an action advertised by the
observed control before using a keyboard shortcut or screenshot coordinates.
On macOS, an input exposing `AXConfirm` / `confirm` can be addressed with
`click` and `action:"confirm"`; a Save/Create button exposing `AXPress` / `press`
can be addressed with `click` and `action:"press"`. The tool name `click` also
covers these semantic actions; a submit does not necessarily require Return.
An advertised action may still fail, so verify the saved entry after submitting.
Use `type_text` for insertion and `set_value` for supported whole-field replacement.
For web content, an AX value echo can differ from the application's DOM state;
verify the actual field/submission behavior. Distinguish duplicate control
labels by role and surrounding state. Never blindly repeat input to compensate
for an unverifiable response.

Screenshot coordinates refer to the Cua source screenshot in the operation's
advertised coordinate frame. AICE's generic image pipeline may resize the image
shown to the model; its media description provides original and display sizes.
If they differ, convert a displayed point to source pixels using those sizes
before calling Cua. AICE does not automatically rescale CUA arguments. Supply
the corresponding `capture_id` where the selected schema accepts it. Do not
invent a capture ID, add screen offsets to window pixels or guess a Retina scale.
For explicit desktop targets, follow the upstream target/coordinate-frame
contract. A cropped, missing or historical image is not fresh visual grounding.
Text-only model runs cannot request screenshots; prefer semantic input when
no current image is available.

The selected schema determines which actions accept tokens, indices, points,
keys, delivery modes or launch options. Do not copy a parameter supported by one
operation into another. Settings and Guard still apply to all calls; native
application/file effects can extend beyond the project workspace.

## Foreground and uncertain results

Background delivery is the default. In `background_only` mode, explicit
foreground delivery and desktop-wide input are unavailable. In
`foreground_allowed` mode, you may explicitly request a supported foreground
operation when appropriate for the user's task, including when a degraded
observation recommends it. AICE does not require a particular preceding refusal
and never switches modes or retries in foreground automatically. Availability
in a schema is not proof of support by a specific platform or application.

On macOS 0.30.4, window-scoped foreground pixel clicks activate the exact
window, move the physical pointer, dispatch through HID, then attempt to restore
the previous front application. The pointer stays at the target. Background
pixel clicks do not move the physical pointer; Tk targets explicitly refuse
that route. Semantic AX actions and their virtual cursor feedback remain
separate from physical-pointer movement.

Distinguish the actual refusal before choosing another step:

- `ambiguous_window_target`: supply the exact observed window, not just its PID.
- `same_pid_keyboard_ambiguity`: process-scoped keys cannot safely select between
  sibling windows. Prefer the observed control's semantic confirm/press action.
  Do not describe this as lost focus or repeat the same background key.
- `ax_unresolved` / `off_space_or_ax_unresolved`: a screenshot may be valid while
  the exact window's AX surface is unavailable. A pixel click is not automatically
  safe. Refresh the target once after it settles, then follow the returned route
  limits and the Run's control mode if it remains unresolved.
- Native session expiry: rediscover the needed tools, then observe fresh state.
  AICE owns session creation; never call lifecycle tools or reuse old tokens.

If supported semantic routes are exhausted in `background_only`, report the
specific blocked step and the Settings → Computer Use control-mode option.
Do not try foreground calls that this Run prohibits or repeatedly switch among
unverifiable inputs. Report command delivery separately from task completion.
Cursor visibility is also separate: missing cursor feedback does not establish
failure, success or lost focus. Do not repeat an input to make a cursor appear.

Before changing route after an error, inspect its execution state and the
application. For `not_dispatched`, correct the reported precondition. For stale
references, obtain fresh state and copy the new references. For `unknown`,
timeout, cancellation or an error after dispatch, effects may already have
occurred. Never repeat a submit, send, toggle or launch solely because its
response was lost. Foreground advice does not establish that an earlier action
had no effect. If the postcondition cannot be established, report uncertainty
rather than claiming completion.

Earlier 0.29.1 native findings remain unresolved until retested on 0.30.4:
macOS background pixel double-click
can disturb focus, right-click can deliver duplicate events, and background drag
is unavailable in the tested AppKit route. Linux Unicode insertion can truncate,
and some GTK background key/gesture routes require unavailable independent input
support. Prefer an equivalent supported semantic control when the task permits;
do not claim an unsupported gesture worked or repeat it blindly.
