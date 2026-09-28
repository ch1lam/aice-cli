---
name: computer-use
description: Operate native desktop apps through AICE's managed:cua MCP service with Cua Driver 0.29.1. Use when that managed service is available for a desktop task; this guide does not apply to the older desktop_* entry points.
---

# Computer Use in AICE

This is AICE's guide for its managed Cua Driver **0.29.1** operations. It is
written for the restricted schemas supplied by AICE, not arbitrary Cua servers
or upstream CLI examples. Loading it supplies guidance, not permission or a
connection. Stay within the user's requested task and existing authorization.

## Find the available operations

Use `tool_search` with `service: "managed:cua"` and the operation you need,
for example `{"service":"managed:cua","query":"list_windows","limit":1}`.
Call the returned model-facing tool name with its complete schema on the next
model round. Raw names such as `click` identify operations; do not guess the
hashed model-facing name. Search selects at most five definitions at a time;
use its returned IDs or next page when needed. Search does not grant execution
permission. If a selected definition becomes unavailable, search again.

If this service is absent or unavailable, report the supplied diagnostic.
Computer Use enablement, installation, OS grants and control mode belong to
Settings → Computer Use. Do not start another Driver, register a second MCP
connection, use action CLI commands or switch to another automation route to
work around a refusal. This guide does not turn on the managed route in a
build that exposes only `desktop_*` tools.

## Discover, observe, act, verify

1. Use `list_windows` to find the intended application's window. When the task
   supplies a current PID, pass it as `pid` on discovery; respect any narrower
   task scope even if the schema allows broader discovery. Use the returned exact
   `pid` and `window_id`; neither a title nor an old transcript's numbers establish
   a current target. Each `list_windows` replaces the admitted window set and
   retires all prior observations, even when filtered by PID. For multiple apps,
   finish observing, acting on and verifying one window before discovering the
   next. Rediscover an earlier window before returning to it. If more than 64
   valid windows were returned, filter by the intended PID before addressing it.
2. Use `get_window_state` on that pair. It returns structured target state,
   semantic elements and optionally a screenshot. Prefer semantic element tokens
   when they identify the intended control. Use `query` to narrow a large tree.
   This operation accepts only `pid`, `window_id`, `include_screenshot` and
   `query`; upstream result notes mentioning `max_depth` or `max_elements` do
   not make those arguments available in AICE's selected schema.
   A text-only model must set `include_screenshot: false`; omission requests an
   image. Capture failure can still leave useful semantic state, but cannot
   authorize coordinates.
3. Perform one action grounded in this latest observation. Supply `pid` and
   `window_id`, plus the current `element_token` or image coordinates required
   by the action. Copy the exact token from this observation's structured
   `elements`; a visible element index is not a token. Never construct a token
   from an index or reuse one from an earlier observation. If the result is
   clipped, use `tool_result_read` on this latest observation's recorded result
   until the needed token is available. AICE owns session and capture identities;
   do not supply them.
4. Call `get_window_state` again and verify the task's actual postcondition:
   the desired field value, visible navigation, saved state or submission
   result. A successful RPC, `returned` state or `effect: unverifiable` alone
   does not prove success. Managed actions do not include an automatic fresh
   observation. Every attempted mutation consumes its preceding observation.

Do not batch two actions that depend on one observation. Observe between input
and submit, between clicks, and after scrolling or dragging. State and
identities belong to the active Run. After cancellation, reconnection or Session
resume, rediscover tools/windows and obtain fresh observations. Reading an old
result with `tool_result_read` does not refresh its tokens or pixel authority.

## Input forms

Use only fields in the selected schema. The managed subset supports:

- `click`: one semantic token or `x`/`y`; left single click by default. Right
  click uses `button: "right"`. Double click uses `count: 2` and a screenshot
  point, not a semantic token.
- `set_value`: exact semantic token and `value`, including an empty string to
  clear a supported field. `type_text` uses nonempty `text` and one token or
  screenshot point. Text is bounded to 16 KiB. Observe to check whether the
  chosen operation replaced or inserted text before doing more input.
- `press_key`: a key such as `return`, `tab`, `escape` or an arrow; `hotkey`:
  an array of modifiers followed by one key, such as `["cmd","a"]`. Supported
  modifiers are `cmd`, `shift`, `option`, `ctrl`, `fn`, without duplicates;
  supported keys are lowercase a–z, 0–9, f1–f12, return, tab, escape, arrows,
  space, delete, home, end, pageup and pagedown. Key operations use a current
  token or the observed window, not a pixel point.
- `scroll`: one token or screenshot point, direction `up`, `down`, `left` or
  `right`, and optionally `amount` from 1 through 50 lines (default 3).
- `drag`: `from_x`, `from_y`, `to_x`, `to_y` grounded in one fresh screenshot;
  optionally `duration_ms` from 1 through 10000.

Coordinates use pixels of the image actually displayed to the model, with the
origin at its top-left. Do not use screen-global coordinates or apply a guessed
Retina/scale factor; AICE maps the displayed image to the native capture. A
cropped, omitted, invalid or historical image cannot ground a new pixel action.

To launch an application, first use `list_apps`, then pass the exact discovered
`bundle_id` on macOS or `launch_path` on Linux to `launch_app`. Launch once, then
rediscover its window and observe it. Missing window state is not permission to
repeat launch. URLs, arbitrary arguments, inspector ports, screenshot file
paths, element indices and alternative target forms are outside this interface.

## Refusals and uncertain results

Background delivery is the default. In foreground-allowed mode on macOS, AICE
may explicitly report a verified refusal before input. Observe that same window
again. Only if AICE's fresh observation permits the continuation may you request
`delivery_mode: "foreground"` for the same action and payload, identifying the
intended target again from fresh tokens or pixels. A generic error or
`effect: refused` is insufficient. There is no automatic foreground retry;
Linux does not currently offer this verified continuation path.

For `not_dispatched`, correct the reported precondition before a new attempt.
If the window identity is no longer admitted, rediscover that window with its
PID, then observe it again. If an element token is stale, obtain and read a fresh
observation of the currently admitted window before choosing further input.
For `unknown`, timeout, cancellation or an error after dispatch, effects may
already have occurred. Re-establish fresh observation and inspect the business
state before choosing any further input. Never repeat a submit, send, toggle or
launch merely because its response was lost. If the postcondition cannot be
established, report the uncertainty rather than claiming completion.
