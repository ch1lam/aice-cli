# Computer Use integration

Computer Use uses pinned Cua Driver **0.30.4**, MCP protocol `2025-06-18`, and
AICE's generic MCP catalog, Guard, media and Session boundaries. When enabled,
Print and interactive main runs discover `managed:cua` through `tool_search`;
selected schemas enter the next model request. The embedded `computer-use`
Skill supplies version-matched guidance on demand. Windows actions are unavailable.

Use `/desktop` or Settings → Tools & Network → Computer Use for enablement,
control mode and setup/repair. The preferences are user-only. Saving a preference
alone does not install a runtime or grant OS access. Installation details and
artifact provenance belong to [Installation](installation.md) and
[VENDOR.md](../internal/deps/cua/VENDOR.md).

## Ownership and connection contract

`internal/deps` verifies the distribution; `internal/desktop` admits the runtime
and owns native sessions, serialization and cleanup. `internal/mcpclient` owns
protocol/framing and its exact subprocess. The application owns settings,
Run bindings and permissions. Cua owns native targeting and input behavior.

Admission requires the pinned identity/protocol and the complete reviewed
schemas of 15 tools, bounded to 16 pages, 256 entries and 4 MiB. Only JSON object
ordering/whitespace are ignored; additional tools are unavailable. Review the
[schemas](../internal/desktop/schema/README.md) together with platform behavior
on upgrades. Matching schemas do not prove native input/capture correctness.

The MCP pipe is capped at 24 MiB per message. Child stderr/SDK diagnostics are
discarded; the environment excludes model credentials, loader injection and
inherited permission overrides. Telemetry and update checks are disabled.
Closing terminates/reaps only AICE's owned proxy/direct child, not a shared daemon
or user application. Calls are never automatically retried.

On macOS, AICE uses a verified signed `/Applications/CuaDriver.app` and explicit
service endpoint. Read-only status, `get_config` and `check_permissions` with
`prompt:false` check standard mode, policy, version, PID, bundle/executable and
OS grants, then recheck service identity. These checks neither enumerate windows
nor establish capture success. Unknown/mismatched policies and missing grants
remain distinct errors. The proxy's `--embedded` flag refuses automatic launch;
AICE does not use `--direct` or impersonate a host bundle.

An enabled cold Run may launch the verified App through LaunchServices only
after the pinned status diagnostic establishes absence. A setup lock and second
status read prevent duplicate AICE launches. The standard-mode launch suppresses
startup permission UI, but does not bypass OS checks. Existing incompatible
services are not restarted/reconfigured and a lost launch response is not retried.

Linux requires known X11 routing and known absence of Wayland. A compatible
shared service must pass status, policy, Unix-peer, executable and MCP checks.
Only the exact `not_running` diagnostic permits an owned `mcp --direct` runtime
in the verified distribution directory with a fixed standard-mode environment.
Other failures never trigger fallback. Missing AT-SPI may leave pixel capture;
Wayland, XWayland and unknown/headless display states are refused.

### Managed MCP boundary

The model sees 11 upstream operations: `list_apps`, `list_windows`,
`get_window_state`, `launch_app`, `click`, `drag`, `type_text`, `set_value`,
`press_key`, `hotkey` and `scroll`. Their schemas retain native options, removing
only `session`, which AICE supplies. Four other reviewed operations belong to
host lifecycle/OS setup. There are no production `desktop_*` model wrappers;
legacy Session presentation remains readable.

| Owner | Enforced boundary |
| --- | --- |
| Application/Guard | Enabled preference, application-owned service identity, selected version and permission before dispatch |
| Desktop Run/Manager | Host session, frozen mode/image capability, published argument fields, serialized calls, cancellation, cleanup and no replay |
| Cua Driver | Application/window targets, element tokens, snapshots, capture coordinates, native arguments, input and refusal details |
| Generic MCP/media/Session | Bounded ordered results, image preparation, exact structured source, durable retention and paged readback |
| Caller/model | Choose from current observations and explicitly verify the application's postcondition |

AICE has no second model-path app/window allowlist, observation-consumption rule,
element registry, coordinate conversion or refusal-continuation state machine.
PID/window references can cross model turns, but do not become valid merely by
appearing in history. Refresh native state when continuing a task and respond to
Cua's stale-reference diagnostics. Local setup's capture/reference validation
is not the model execution contract.

`background_only` rejects explicit foreground delivery and desktop-wide input.
`foreground_allowed` permits an explicit foreground request without requiring a
prior refusal. It does not prove route availability. There is no automatic
foreground fallback. Text-only models must use `include_screenshot:false` and
cannot request image-output files. Native launch arguments, targets and output
paths remain available where the pinned schema permits them; GUI/native file
effects are outside workspace file policies.

Coordinates are Cua's source coordinates. Generic media may resize the displayed
image and describes both sizes; AICE does not reverse that transform in calls.
For semantic work, focused queries and `include_screenshot:false` reduce output;
queries do not guarantee a cheaper native tree walk. Clipped structured results,
degradation and escalation advice remain readable through `tool_result_read`.
A successful RPC/AX echo is not proof of the task's business result.

Discovery explicitly admits the connection/session. Calls do not reconnect.
Guard is checked after the native queue and again before transport dispatch.
Transport/catalog failures and recognized native session-expiry results retire
the connection; later explicit discovery starts a fresh lifecycle. Original
results remain unchanged and input is never replayed. Ordinary domain errors
and degraded observations do not trigger connection retirement.

`managed:cua` identity is constructed only from the application's live Run,
pinned runtime and settings owner. Its permission scope includes frozen mode,
reviewed operations and restrictions. Enabling/mode publication or global MCP
restriction changes invalidate old catalogs/permits; unrelated MCP edits and
reconnections do not. Source `*` restrictions apply to managed CUA. Ordinary
configuration cannot claim managed identity or its endpoint; see
[MCP admission](mcp.md#implementation-status).

MCP status always displays the Computer Use preference without native I/O.
`/mcp desktop` navigates to its setting. Ordinary MCP mutation commands cannot
edit this managed entry. Discovery and the Skill create no extra permission
exemptions; future upstream tool additions are not automatically authorized.

### Version-matched guidance

The embedded Skill is AICE-authored guidance for the pinned version. Normal
Skill discovery/trust/precedence applies; remote instructions and enabling
Computer Use do not auto-load it. The guide describes selection, native reference
and coordinate handling, explicit post-action checks, unknown outcomes and the
configured control mode. Overriding the guide cannot override host/Guard policy.

## Run, reference and result contracts

One application Manager binds each main Run with frozen mode/image support and
no native I/O. First discovery starts a unique native session. Run close cancels
and ends its own session; the Manager can reuse the connection until disconnect
or application close. Side questions remain tool-free. Old Session references
are historical and reading retained results cannot revive them.

The pinned native session defaults to five idle minutes plus a thirty-second
maintenance sweep. Sessionless discovery uses a transport-owned implicit session;
AICE sends no keepalives. Explicit rediscovery after recognized expiry establishes
a new identity. Cancellation/lost responses can leave effects unknown: observe
before deciding further input, and do not interpret an unreturned action as
unexecuted.

On macOS, first desktop use takes an exclusive AICE occupancy lock at
`~/Library/Caches/cua-driver/.aice-desktop.lock`, before runtime admission. It
waits up to two seconds and responds to Stop. It spans model decisions and
connection recovery, until the last desktop-using Run releases it. An idle
connection holds no lock. Setup uses the same occupancy before its setup lock;
read-only status does not. Kernel lock ownership, not the persistent file's
presence, signals occupancy. This coordinates AICE instances, not third-party
clients or user input, and cannot prove an unknown native request has stopped.

### Production presentation

Text Print progress shows tool names, outcome and elapsed time without copying
input/query bodies to stderr. Session and explicit NDJSON transcript output can
contain those arguments, window contents and images. The TUI shows a Computer
Use activity row and folded tool details. Requested route, `Returned` and model
Planning are presentation facts, not proof of delivery or task success.

Stop remains Stopping until completion; absent terminal results show Result
unavailable. Completion and branch replacement clear live activity. Replaying
history derives display from source records without native I/O or a live runtime.

## Settings and task continuation

Preference reads perform no native inspection. The TUI separately requests one
bounded status refresh on open/manual refresh/save/action completion, not a poll
loop. It cancels superseded reads and discards old panel/configuration results.
macOS inspection reuses only an installed verified App and existing service;
it neither downloads, launches nor creates a session. Connection, OS permissions,
model image support and historical capture are separate facts. `Last capture
succeeded` is a timestamped setup result, not a readiness guarantee.

Explicit macOS setup verifies installation/service identity before public
`permissions grant`, then performs fresh admission. The public command can open
system UI and includes a live capture probe. Setup never resets TCC or stops a
shared daemon. Cancellation cannot retract grants or close already opened UI.
Task tools and read-only settings cannot request grants.

Linux setup installs privately when needed, admits a temporary production
Manager, then offers bounded window metadata with Cancel selected. Only an
explicitly selected target triggers one validated screenshot. The local image
is discarded without model/Session exposure. Setup performs no input, full-desktop
capture, system-package installation or invented OS grant. Its four-minute
deadline includes selection; all exits close owned sessions/connections.

Setup holds the existing Settings reservation through external work and ordinary
save/publication, but no settings-file lock during native calls. Installation,
authorization/capture, saved preference and applied runtime are distinct result
facts. A later save failure retains external success and offers preference-only
retry. Failed save leaves the previous runtime active; successful publication
invalidates old managed authority. Disclosure is paged before choices become
active and describes host access, provider exposure and actual application effects.

`/desktop` opens the same setting during a Run. Settings Stop/F6 uses ordinary
cancellation; Esc closes only the modal. Neither stops the shared service nor
changes enablement. After successful setup/enable, a nonempty recorded task may
offer an explicit Continue/F6 button: it starts a fresh Run against recorded
progress, instructing fresh observation and no replay of completed/uncertain input.
It does not send composer drafts/attachments or queued follow-ups. The proposal
is bound to Session, leaf and settings revision, revalidated at preparation and
admission, and recorded only on explicit submission.

## Platform evidence

Tests and commands are maintained in [Verification](collaboration.md#computer-use-checks).
Most native action evidence was recorded with **0.29.1** and sometimes the removed
typed adapter; it is not acceptance of 0.30.4 or the current forwarding path.
The current code/dependency pins, schema review and offline tests establish
implementation, not native task success.

| Platform | Retained evidence | Outstanding scope |
| --- | --- | --- |
| macOS | 0.30.4 signed artifact/schema/absent-service checks and same-Run read-only reconnect; current thin adapter's scripted AppKit/WebKit/AppKit and discovery-expiry gates passed on 0.29.1 | Current-version real-model/real-app tasks, degraded-state foreground recovery, first installation/system-dialog interaction, physical input/IME, interrupted gestures and multiple displays/Spaces |
| Linux arm64 | 0.30.4 private installation/reuse; earlier 0.29.1 isolated X11/GTK owned/shared runtime, semantic edits, selected capture/setup and scripted CLI/Session evidence | Current thin adapter/version action acceptance, native Unicode/launch failures below, physical desktop/IME and other toolkits/compositors |
| Linux amd64 | Artifact hashes, ELF inspection, synthetic tests and cross-compilation | Native installation, service admission, capture/input |
| Windows amd64/arm64 | Artifact hashes, signature/peer/status implementation, synthetic tests and cross-compilation | Native status/signature trust/UIAccess/session validation; setup/action integration is unavailable |

The 0.29.1 scripted three-form pass verified exact values, one commit per window,
nine images, no sentinel focus loss, shared-service survival and Session replay.
Earlier unfiltered attempts exceeded context limits; one had unattributed focus
loss. Independent passing fixtures do not erase failures or prove efficiency.
The current boundary's discovery-idle gate also recovered after six idle minutes
without input/capture or a daemon restart. An operator-reported older
TextEdit → Safari → VS Code task succeeded with VS Code accessibility mode enabled,
then encountered discovery failure; it is historical evidence only.

A 0.30.4 synthetic visual diagnostic committed exactly once through semantic and
screenshot-coordinate requests on primary and left secondary displays, with no
sentinel losses. Coordinate requests also used AX, so this does not prove raw
pointer delivery. The cursor was visible on the primary display but absent on
the secondary; the renderer covered only the primary display. The diagnostic
bypassed a failing stock lifecycle session-label assertion, so the stock cursor
gate remains unaccepted on 0.30.4. AICE does not move windows or adjust coordinates
to compensate for overlay feedback.

### macOS input limitations

Earlier 0.29.1 gates found temporary focus loss on background double-click and
duplicate right-button event pairs. Both used one RPC and returned
`effect:unverifiable`. Background scroll passed; background drag refused without
movement. Explicit foreground drag passed once but later failed movement/focus
restoration. These are unresolved acceptance findings, not proof of identical
behavior on 0.30.4. The current version's foreground pixel route can move the
physical pointer and leave it at the target; it is never an automatic fallback.

WebKit `set_value` echoed AXValue without changing the fixture DOM; `type_text`
on an empty field completed that fixture while still reporting unverifiable
effect. Confirm application state independently. Earlier pixel/resize and
single-display negative-origin checks do not cover mixed-scale monitors, crops
or generic source/display conversion on the current boundary.

### Windows action admission gaps

Only read-only inspection exists. It checks pipe-server PID, executable, user
SID, login session and creation time before/after MCP exchange. It does not
start/elevate a service. Constant UIA/PostMessage flags and a nonzero login
session are not evidence of unlocked, usable input. Native checks remain open.
Action acceptance must establish launch/window ownership across same-name
processes, launcher handoffs and packaged hosts before adding an action runtime.

### Linux input acceptance failures

On 0.29.1, GTK Unicode `type_text` returned success but truncated a 17-byte,
11-character request to ten bytes/eight characters. A source/control experiment
identified character-count versus UTF-8 byte-count insertion length. `set_value`
uses another route and does not prove insertion correct. This needs current-pin
reverification, not silently reclassification as accepted.

The isolated Xvfb fixture without `/dev/uinput` refused GTK background key/hotkey,
pixel scroll and drag with unchanged widgets. Button clicks may take semantic
routes and do not establish general input readiness. Do not attach host input
devices merely to pass the test. Native capture validity facts pass through the
generic result; there is no second AICE pixel-admission check.

### Linux launch acceptance failure

The earlier XDG launch fixture completed its task and survived Manager close,
but stole foreground activation despite `active:false`. That field was not a
focus measurement. Concurrent test keys survived, proving focus interference
rather than observed keyboard misdelivery. Current-version and other window
manager/D-Bus behavior remain unverified.
