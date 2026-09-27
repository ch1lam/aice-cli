# Computer Use integration

Computer Use is being integrated with Cua Driver. The current implementation
contains pinned distribution metadata, a persistent stdio client, User-only
configuration fields, and application-owned desktop run bindings. When enabled
in user configuration, Print and interactive main runs expose `desktop_apps`,
`desktop_observe` and `desktop_act` through the existing Loop and Guard.
On macOS, Settings → Tools & Network → Computer Use opens an explicit setup
flow when enabling. The setup/repair row also offers preference-only saving;
the preference alone does not install or authorize a native service. An enabled
run may lazily start the already verified App without permission prompts.
Linux Settings uses the same entry point for private installation, X11 connection
checks and an explicitly selected window capture. Verification images stay local.
The implementation plan's complete native acceptance remains open.

## Ownership and connection contract

`internal/deps` owns immutable artifact selection; see
[provenance and licensing](../internal/deps/cua/VENDOR.md).
`internal/desktop` owns the Cua connection and its exact child handle. The
application owns configuration publication and run bindings. The existing
Agent Loop, Guard, media pipeline and Session remain the execution boundaries.

The client uses official Go MCP SDK v1.6.1, sends legacy `initialize` with
`2025-06-18`, checks the returned protocol and Cua identity/version, then discovers
tools once with bounded pagination. Before any `tools/call`, it compares the
complete input schemas of all 15 used tools against the corresponding macOS or Linux
[reviewed 0.29.1 inventory](../internal/desktop/schema/README.md). Only JSON
object ordering and whitespace are ignored; missing tools or changed fields,
required parameters, defaults, enums, bounds and target alternatives reject the
connection. Additional upstream tools remain unavailable to the private client.
A version/platform update requires reviewing both its schema pin and adapters.
This validates the advertised contract, not actual native behavior.
It does not mix modern `server/discover`
or per-request protocol metadata into that session. Responses retain text,
image bytes, structured content and domain error status. No tool call retries
at this transport boundary, including after cancellation, timeout or EOF.

Stdout is a private NDJSON pipe capped at 24 MiB per message before JSON/base64
decoding. SDK diagnostics and child stderr are discarded rather than duplicating
window contents or typed text in logs. Process environment construction excludes
model credentials, loader injection and inherited Cua permission overrides.
Owned children disable Cua telemetry and update checks.
Closing the connection waits for or terminates only its owned MCP child; it does
not stop a shared service. The shared-service path requires an explicit endpoint.
The proxy uses the pinned release's `--embedded` switch solely to refuse
automatic service launch if that endpoint disappears. On macOS AICE never uses
`--direct` or claims a host bundle identity; the standalone daemon retains its own TCC
identity and permission mode.

Linux reuses a compatible existing service only after status, policy, Unix peer,
executable and MCP checks. Only the exact pinned `not_running` diagnostic permits
an owned `mcp --direct` child, which the public Linux CLI supports. Other status,
identity or policy failures never fall back to a new runtime. That child has a
fixed standard-mode environment and validates upstream native policy at startup;
it creates no shared daemon, autostart entry or implicit OS grant. Its working
directory is the verified distribution directory and its lifetime belongs to
the Manager connection. Both paths require known X11 availability and known
absence of Wayland routing. Unknown display facts, Wayland and XWayland are
refused until their background-input adapters have been reviewed. Missing
AT-SPI can leave a pixel-only route, whose actual capture is checked separately.

A private macOS connection preflight now checks the existing service before
admission. The bounded public `status --socket` command supplies content-free
mode, policy and PID facts; its pinned text format is parsed only for management,
never for desktop action results. The persistent MCP connection then checks
`get_config` for the actual daemon version/platform and `check_permissions`
with `prompt:false` for App executable, bundle identity, PID and OS grants.
A second status read rejects a changed service. External policies/manifests,
non-standard mode, identity mismatch and missing grants remain distinct errors.
No admission probe enumerates windows or captures; granted TCC booleans are not evidence
of successful capture. Cold application connections use this preflight after
verifying the installed App. Settings calls the explicit native setup API;
native authorization acceptance remains outstanding.

On macOS, Settings' Computer Use status row uses a bounded read-only refresh (eight seconds
for installed-App verification and inspection, with a five-second inspection
deadline). It reuses only an already installed, verified App; it never starts a
daemon or downloads. A separate short-lived MCP proxy reads `get_config` and
`check_permissions` with `prompt=false`, verifies the same service identity and
standard-mode contract, then closes only that proxy. No session or observation is
created, so this refresh cannot replace the task's executable window snapshot.
Missing grants remain distinct from Unknown. An absent service remains Stopped
with Unknown permission facts, rather than reading the terminal's grants.

The status details separate connection, Accessibility, Screen Recording, model
image support, and capture verification. Capture results come only from explicit
setup verification or actual requested task screenshots and carry a timestamp.
They are labelled historical, not a guarantee for the next capture; upstream
historical `screen_recording_capturable` fields do not establish current readiness.
A successful historical capture is labelled `Last capture succeeded`, not general
input readiness. Status details disclose the pinned macOS double/right-click and
drag limits and the measured Linux typing/input/launch limits. Model action
guidance carries the same limitations, separately from individual Driver results;
it does not turn a refusal into success or authorize foreground fallback.
The feature's enabled preference remains separate. Preferences load without
native inspection; after displaying them, the TUI requests status separately and
updates only that row (including its open details). Opening or manually refreshing
the panel, saving a preference and completing a domain action each request one
check, not a poll loop. Closing, refreshing, saving or starting a domain action
cancels the preceding check; shutdown cancels and waits. Cancelled results and
results for an older panel generation or configuration revision are discarded.
Status reads hold no Settings write reservation and never advance the
configuration revision. Inspection failures stay in the status row and do not
block other settings or overwrite save feedback.
The native no-autolaunch check passed with the pinned App binary, a temporary
HOME and an absent socket. No user service was connected or started. Default
tests cover changing service identity, missing grants, mode/policy rejection,
inspection schema checks, bounded subprocess output and cancellation.

## Run, reference and result contracts

A run binding freezes its control mode and model image support without native
I/O. The manager serializes complete action/observation sequences. It lazily
starts a uniquely named Driver session, ends only that session on run close,
and keeps its connection available until disconnect or manager close. A cached
status read never enumerates, captures, launches or requests authorization.
Public manager construction requires an application runtime resolver. It only
reuses a verified installation and admits a compatible service; a pinned
proxy alone cannot prove a shared daemon's version or permission mode.

Failed or malformed app/window discovery retires the manager's connection and
all execution references. An observation that cannot establish usable state for
the exact target does the same, including a native domain error over a still-live
MCP pipe. A later explicit discovery re-admits the service and starts a fresh
session; the failed operation is not retried internally. A returned mutation
keeps its original outcome and details if its follow-up observation fails.
Valid target-bound partial observations retain their available semantic
references, and a failed screenshot alone does not retire a usable semantic
observation. Only AICE's connection is closed, not the shared daemon.

Cua's native lifecycle session can expire independently of the reusable MCP
connection. The pinned runtime defaults to five minutes of session inactivity
with a thirty-second maintenance sweep. AICE does not send keepalives or silently
revive expired labels to replay an action. The native expiry gate passed with
race detection on 2026-09-27: the session disappeared after 5 min 20 s; the old
token returned a native error with no widget commit or fresh observation. Its
unusable follow-up observation retired the connection. A new run established
a second connection, discovered/captured the same fixture and committed once.
The returned error outcome was preserved, consumed references were rejected,
and the shared daemon retained its process and standard-mode policy. The full
gate took 335.95 s. This verifies natural expiry and new-run recovery, not daemon
restart, permission revocation or foreground/IME coexistence. See
[the expiry procedure](collaboration.md#native-session-idle-expiry).

On macOS, the first desktop use also obtains an exclusive AICE occupancy lock
at `~/Library/Caches/cua-driver/.aice-desktop.lock`, before runtime resolution or
connection. The lock waits at most two seconds and responds to Stop; contention
returns `desktop_busy` without native discovery or input. It spans the run's
model decisions and connection recovery, so another AICE instance cannot replace
the pending observation. Multiple bindings in one manager share ownership until
the last desktop-using run closes. An idle reusable MCP connection holds no lock.
Explicit setup obtains the same occupancy before its separate startup/setup
lock; read-only status and Settings refresh do not acquire occupancy.

The lock follows the Session writer's OS file-lock pattern. Its persistent file
is never deleted or treated as proof of a live process; the kernel releases
ownership when the handle closes or the process exits. Run/manager cleanup
releases it after local in-flight work settles. If Stop's cleanup deadline expires,
the execution owner performs the deferred cleanup on return and then releases
occupancy. This coordinates AICE instances only; it neither isolates the user or
third-party Cua clients nor proves that a lost native request has stopped inside
the shared daemon. Unknown action results remain unknown.

On macOS, an enabled cold run starts the signed `/Applications/CuaDriver.app`
through LaunchServices only after the pinned `status` command establishes an
absent daemon. A bounded per-user setup lock serializes AICE instances and a
second status read avoids duplicate launch. Unknown failures, policy conflicts
and existing incompatible services never trigger restart or reconfiguration.
Launch uses `serve --permission-mode standard --no-permissions-gate`: the last
switch suppresses unsolicited startup permission UI, not OS access checks.
Telemetry/update opt-outs and non-embedded mode are explicit. LaunchServices
owns the daemon; AICE owns only its management/proxy children. A lost launch
response is not retried automatically.

The explicit macOS setup API uses the same lock, checks service mode/version
and signed daemon identity before requesting `permissions grant`, then performs
fresh read-only admission. The pinned public grant command includes an explicit
live capture probe; its successful completion is retained as a point-in-time
fact even if subsequent admission fails. Setup reports requested/completed
external steps separately from readiness. It never calls private permission
helpers, resets TCC, or stops a shared daemon. Cancellation stops AICE's command
but cannot retract grants or guarantee closure of already opened system UI.
Only the Settings setup action invokes this API after disclosure;
model tools and read-only Settings views cannot request grants.

Linux setup creates a temporary Manager through the production runtime resolver,
holds the existing desktop occupancy reservation, and discovers bounded window
metadata. The Settings menu starts with Cancel; only an explicitly selected
opaque target reference can trigger one screenshot. The same media validation
and capture binding used by task observations must succeed. No click, typing,
full-desktop capture, system package installation or permission change occurs.
The verification image is discarded locally, never sent to a model or recorded
in a Session. The temporary native session and owned connection close on success,
selection cancellation, stale target, capture failure and deadline; shared
services and user applications remain running. One four-minute deadline includes
selection and verification. An empty window list asks the user to open a window
and retry; setup never chooses a different target automatically.

Connection and selected-window capture are separate external-step facts on Linux.
Capture success is retained as historical evidence even if saving enable later
fails. No macOS-style authorization grant is invented. Wayland/XWayland and
headless displays fail before window selection, with a display diagnostic.

The Settings flow describes access beyond the project, model-provider exposure,
real application effects, platform-specific installation and permission behavior,
and the explicit capture test. Cancel is the initial choice. Long descriptions are
paged before confirmation choices become active, with shared mouse/keyboard
geometry; resizing restarts disclosure pagination. Cancelling ignores late
prompts and leaves the conversation composer/history untouched.

Setup owns one shared Settings reservation throughout installation, authorization
and the ordinary prepare/save/publish path. It holds no settings file lock during
native work. Installation, authorization, saved preference and applied resources
remain separate result facts, shown in a scrollable result page. Failed saving
retains external success and offers preference-only retry. The captured startup
helper-download policy still applies after a restart-only setting is saved.
Before requesting grants, setup retires the idle manager's connection and refs
so the next run admits a fresh connection. Text-only models retain semantic
access but the result describes unavailable image/pixel capability.

The application constructs one manager without native I/O and binds it only
when a main run actually starts. The context carries both the owner identity
and frozen run backend; closing a run cancels that context before bounded
session cleanup. Print and interactive shutdown also close the manager.
Settings reads and input preparation create no native run. Tool arguments cannot
change control mode, install helpers or request authorization. Side questions
retain their existing tool-free model boundary.

Startup, Web replacement and desktop preference changes share one tool
composition function. Settings prepares the candidate Loop/tools/prompt before
saving and publishes with the Guard toggle under its existing shared-resource
reservation. A failed save leaves the previous snapshot active. The application
Guard requires a live binding from the same owner; the intrinsic Guard requires
the global toggle. Missing/closed bindings and disabled capability are hard
denials, including under `--yolo`. This does not enforce workspace file policies
inside native applications; [Guard behavior](execution-sessions.md#tool-execution-boundary)
describes that boundary.

Window discovery issues opaque references for returned native pid/window pairs.
Application discovery also returns bounded installed/running app identities,
including localized names; bundle-ID matches include their running windows.
Launch accepts only a locally issued app reference, resolves its known macOS
bundle ID or Linux discovered XDG command, and consumes that reference before
one native request. Linux launch never accepts a command or extra arguments
from the model; its response must establish a running PID and the same discovered
command before window binding. A ready single window
is observed immediately; multiple windows remain explicit candidates. If the
process is known but its window is late, a bounded five-second read-only wait
discovers that PID's windows without launching again. A lost response never
triggers another launch. Launch itself uses Cua's launch path. The Linux native
launch gate observed foreground focus loss despite the Driver's `active:false`
response; see [launch acceptance failure](#linux-launch-acceptance-failure).
The macOS AppKit cold-launch gate preserves its foreground sentinel and returns
multiple candidates without choosing one; heterogeneous application launch and
physical-user coexistence remain separate acceptance.
Offline launch-wait tests keep an unrelated window with the same document title
present while the target is delayed. Only the launched PID may supply candidates.
The tests cover target arrival, the five-second deadline and cancellation: a
failed wait retains the launch result without observing another app or replaying
launch, and subsequent explicit discovery remains usable. These checks validate
the local binding/lifecycle contract, not native launch focus behavior.
Observation references bind the run, connection generation, exact target,
Driver snapshot, opaque element tokens and immutable capture ID. Rediscovery,
same-window observation (including from another run), action dispatch,
cancellation and disconnect invalidate the relevant old references. There is
no process start-time claim beyond the evidence Cua exposes.

The current typed actions cover click/double/right-click, semantic or pixel
text insertion, semantic value setting, exact-window keys/hotkeys, semantic or
pixel scroll, and left-button dragging inside one observed window. Drag takes
two screenshot points and a bounded duration (default 500 ms, maximum ten seconds).
The pinned macOS Driver refuses background drag; AICE preserves that refusal.
Key names and modifier combinations are validated before dispatch; unrelated
action fields are rejected. Each mutation consumes its observation before
dispatch and returns its Driver facts plus a fresh observation under the same
execution reservation. Post-observation failure preserves the action response.
A lost response returns `outcome=unknown`; it never retries the mutation.
`outcome=returned` means an RPC response arrived, not that a business effect was
independently confirmed. Driver `isError`, structured details and bounded text
remain separate from transport and follow-up-observation failure.

The reviewed macOS foreground assistance requires the user-selected `foreground_allowed` mode,
frozen when the run starts. Input still defaults to background. After a reviewed
pre-input refusal, a successful follow-up observation can expose
`foreground_action_available`. The main Agent may then explicitly request
`delivery_mode=foreground` using that observation, unchanged action content and
the same target form. It must identify the intended element again from the new
tokens or re-ground both drag points in the returned image; AICE does not claim
cross-snapshot element identity. A pixel refusal that permits foreground requests
a fresh screenshot even when the original call did not request a post-action
image. Missing or invalid screenshot bindings suppress that opportunity.
Foreground delivery
may activate the addressed window and change focus. No automatic fallback runs.
The opportunity expires with its observation, including ordinary refresh,
another run's same-window refresh, dispatch, cancellation or disconnect.
It cannot change the run's control mode or authorize another window or action.

The pinned macOS refusal classifier currently covers semantic Electron
`type_text` with `background_unavailable`, Screen Sharing `type_text`/`hotkey`
with `SCREEN_SHARING_REQUIRES_FOREGROUND_HID`, and window-only `key`/`hotkey`
with `same_pid_keyboard_ambiguity`. Each requires an error response and
`effect=refused`; Electron and same-PID keyboard refusals also require matching
PID/window identity (Screen Sharing's early refusal omits these fields).
It also recognizes the pinned `scroll` Electron refusal and `drag` background
refusal: these return only `code=background_unavailable`, before target resolution
or input, without an effect or identity field. This is a method-specific source
contract, not a general rule that a missing effect means no input occurred.
Only these reviewed paths establish that input did not run. Generic advice,
unknown codes, partial/unverifiable effects, lost responses and failed follow-up
observations never create an opportunity. `set_value`, launch and wait do not
accept delivery mode. New platform/version admission must re-review the classifier.
Linux currently retains background refusals without creating a foreground
continuation; macOS refusal codes do not authorize input on another platform.

Semantic condition waits hold the same executor and repeatedly observe the
exact window within a caller-selected deadline of at most ten seconds. They
report `satisfied`, `unsatisfied` or `unknown`; a missing match in an incomplete
projection stays unknown. Polls request no images. A requested final screenshot
is captured once only while time remains, and its semantic condition is checked
again. An expired deadline can return the last valid semantic observation;
a failed refresh never returns older execution references. No hard sleep or
image-stability heuristic stands in for a condition.
The pinned Linux `elements` array contains actionable nodes only, even when
the AT-SPI walk is complete. Its AICE projection therefore remains incomplete:
positive text matches are evidence, but absent passive labels remain unknown.

Observations project at most 200 semantic elements and 96 KiB of their text,
with visible truncation/incompleteness. One screenshot goes through the existing
media validator and image content pipeline. Coordinate mapping reverses only
AICE's image resize. It requires matching actual image dimensions and a native
capture ID; it never adds screen offsets or reapplies Retina scaling. A failed
image can leave valid semantic references available. A text-only model cannot
request a screenshot or obtain a usable pixel binding.

Pixel clicks send Cua's immutable `capture_id`. The pinned macOS `scroll`,
`type_text` and `drag` schemas do not accept that argument; their pixel routes
resolve the authoritative latest screenshot through the same public session,
PID and window used for observation. AICE requires both its verified image
mapping and a non-empty snapshot, consumes the observation once, and keeps the
whole action/observation sequence serialized. Cua refuses a snapshot replaced
by another owner, retired by session cleanup, or refreshed without a screenshot.
AICE never adds private `_session_id` fields or supplies unsupported capture
parameters. These routes do not provide immutable capture-ID checking, and
native geometry-change acceptance remains outstanding for these routes. The
macOS AppKit foreground-drag gate below establishes one explicit continuation
path, not foreground acceptance for all tools or surfaces.
Both point axes must be explicit numbers; omitted axes never become zero.

Tool adapters serialize bounded domain facts as JSON text and append the actual
image as an existing image content part. They preserve partial/unknown dispatch
facts and any follow-up observation even when a later error occurs, marking the
result as an error without replacing it with a generic Go error. Images and
originals continue through the existing provider projection and Session JSONL.
Built-in tool guidance asks the model to distinguish same-name semantic elements
by role. For macOS web-content text fields it prefers `type_text` over direct
`set_value`, and requires checking the rendered result because AXValue read-back
can echo a write that never reached the page. This guidance does not replace
the frozen control mode or turn an unverifiable result into confirmed success.

Text `--print` progress reports desktop tool names, status and elapsed time;
it omits argument details so input text and observation queries are not copied
to stderr. Full calls and results remain in the Session. Explicit
`--output-format json` retains the existing [NDJSON event contract](contracts.md#print-ndjson-events),
including arguments and bounded result text; it is transcript output and can
contain window contents, not a content-free diagnostic stream.

`desktop.Act` returns local `ActionTiming` evidence alongside its domain result:
executor queue admission, mutation RPC, condition/window polling, final observation,
and total call time. Polling includes read-only RPCs and timer intervals; final
observation includes capture, decoding and image processing. The mutation RPC
measurement includes transport overhead and connection retirement on failure;
it is not a measurement inside Cua's native input implementation. Total also
includes local validation and release cleanup. Durations remain available after
failure or cancellation, and no action is retried to obtain a measurement.
These fields are excluded from tool JSON and Session records, with no background
sampler or additional telemetry. Native Manager acceptance tests print only
operation names, synthetic target indexes and these durations. Model output
wait, Guard time and next-request preparation remain outside this boundary.
The [opt-in model gate](collaboration.md#explicit-real-model-desktop-gate) now
measures those intervals in its report alongside the Manager phases, with
explicit provider/Loop/Session boundaries. Its scripted native run validates
measurement coverage and protocol preservation. One authorized real-model run
recorded provider and tool timings but exhausted its reported-token budget before
any Commit click was dispatched; full task acceptance, isolated network timing
and controlled cold/warm comparisons remain unverified.

Interactive runs show one Computer Use activity row above the composer. It
uses application-projected tool events: discovery/observation, a requested
background or foreground route, a condition wait, and model Planning between
calls. A request label does not claim native dispatch or successful delivery.
The app name comes only from returned discovery metadata matched to the local
reference; unknown names are omitted. A bounded run-local display cache owns
these names and never authorizes execution or polls the desktop.

Tool headings show the same concise identity and recorded outcome; parameters
and bounded result details stay under the existing detail fold. Unknown outcome, missing
setup, failed/incomplete observation and unmet/unknown conditions remain
explicit instead of changing to a success label. `Returned` means only that a
non-error response was recorded. Stop displays Stopping until completion;
missing terminal tool results become Result unavailable. Run completion and
branch replacement clear live activity, and unrelated tools/BTW presentation
use their ordinary status. Narrow layouts prioritize the phase and escape
application names before rendering.

History reuses the result projection from original Session records without
starting a runtime or synthesizing live progress. Display names and phases are
not additional Session truth. Native overlay/input evidence remains separate
from this TUI activity indicator.

Other foreground routes remain to be implemented. Loop wiring and schema
rejection are covered by scripted-model and raw MCP tests; they are not a claim
of native readiness.

## Baseline and outstanding integration

At implementation start, main was `f64611e`; Settings configuration, application
coordination and TUI were committed in `a61649f`, `bd3d949`, and `0e531b9`.
There were no staged/unstaged source changes. Untracked plans were preserved.
The earlier Settings plan is absent and was not restored.

Reusable boundaries: `Config.WithPatch` / `SaveSettingsPatch`,
`SettingsReader` / `SettingsWriter`, `beginSettingsOperation`, and the existing
five-category Settings panel. Configuration source filtering now excludes auth,
project, environment and flag input for both desktop preferences, including
case variants. Reset inherits only the user/default source chain.
Settings actions return structured external steps, commit/application facts,
known readiness, revision and warnings. The panel retains those facts alongside
a later error instead of replacing success output. Ordinary patch preparation
and publication also have an internal entry point under the existing reservation,
so setup does not acquire a second reservation.
`/desktop` opens this same panel at Computer Use, including during a run; it
does not enter the transcript or prompt history. The Settings footer's explicit
Stop current run button (mouse or panel-local F6) uses the existing cancellation
path, including preparation before the cancel callback arrives. It stays in
Stopping until run completion. Esc only closes the current modal level. No
stop action changes the enabled preference or stops the shared service.
After successful setup or preference-only enable, an existing recorded task gets
an explicit **Continue** button (panel-local F6) on the action result page.
The page previews the new request: continue from recorded progress, observe the
current application state, and avoid replaying completed or uncertain actions.
Setup never starts that request automatically. Choosing it creates a fresh run
against the existing Session context; composer text, file references and images
remain unsent, and old queued follow-ups are not transferred. The proposal is
transient and disappears when leaving the result page. It records no Session
entry until explicitly submitted through the ordinary run path.

The application binds each proposal to the Session ID, selected leaf and Settings
revision. Both input preparation and run admission revalidate it, rejecting
changed settings, branches, Sessions or a proposal already consumed by a recorded
run. Empty Sessions do not offer continuation. A rejected preparation removes its
optimistic transcript entry without replacing the preserved composer draft.
Native acceptance and the remaining platform implementations remain open.
A Web rebuild must retain the same Desktop binding
as the tools and prompt it publishes; offline publication tests cover this.

## Platform evidence

| Scope | Evidence | Remaining acceptance |
| --- | --- | --- |
| macOS 0.29.1 universal artifact | Verified App installed and both OS grants enabled by the operator; signature, Gatekeeper and 15-tool admission checks pass; native three-AppKit Manager and scripted-model CLI/Guard/Session gates pass with nine captures and foreground sentinel intact; ASCII/Unicode insertion, single key, select-all, Retina pixel click, resize refusal/recovery and background scroll pass independent widget checks; AppKit cold launch preserves focus, returns multiple candidates and completes the explicitly selected window's task; cursor renderer lifecycle and isolated host-surface appearance verified; wait and dispatched-click cancellation pass without input replay; Settings Stop cancels a native wait and an in-flight committed click with exact unknown-result Session retention; one explicit foreground-drag run succeeded with a measured focus transition and restoration; actual Settings repair reuses the installation, completes the public grant/capture check and saves temporary enable | Pixel double-click loses foreground focus and pixel right-click delivers duplicate event pairs; background drag is refused by 0.29.1 and foreground-drag repeatability remains open; first-time Settings installation and system-dialog interaction, overlay compositing/animation, interrupted gestures, remaining pixel actions, heterogeneous applications and physical input/IME coexistence; actual-model and broader performance acceptance |
| Windows amd64/arm64 | Downloaded archives and selected executable hashes verified; private installer with Authenticode checks implemented; synthetic extraction/reuse/cancellation tests and cross-compilation pass; static imports inspected; read-only service inspection and Windows status presentation implemented with synthetic tests | Native installation/signature trust, exclusive publication, named-pipe identity/UIAccess/session checks and status-schema confirmation; setup/action runtime integration and native UI/input/lifecycle tests |
| Linux arm64 | Private installation/reuse and read-only headless inspection passed in an isolated Debian 13 container; production Manager and selected-window setup passed owned stdio and verified shared-service X11/GTK checks; scripted-model native print/Guard/tool/Session flow, ASCII insertion and pixel click/resize rejection passed; launch established its exact window and preserved the app after Manager close; actual Settings CLI flow passed native private installation, selected-window capture, cancellation/retry and saved enable | Launch steals focus, Unicode insertion truncates and GTK key/hotkey, pixel scroll and drag are unavailable in the fixture; actual-model tasks, physical terminal/IME and other desktop environments; other pixel actions, foreground assistance, overlay, other toolkits, real compositor/Wayland and physical-input/IME checks |
| Linux amd64 | Downloaded archive and selected executable hashes verified; synthetic installer tests and cross-compilation pass; ELF library dependencies inspected | Native installation/dynamic loading and exclusive publication; runtime/service admission, AT-SPI/display detection, compositor-specific input/capture/overlay tests |

On 2026-09-27, after explicit operator approval, the native host's pre-existing
0.7.0 App was replaced with the verified **0.29.1** distribution. No old service
was running. The replacement retained the official signature and passed signing
identity and Gatekeeper checks before and after publication; the installed CLI
reports 0.29.1 and its 15-tool metadata inventory matches AICE's pin. This was an
operator-authorized host upgrade, not proof of the Settings installation workflow.
The production installer still preserves incompatible pre-existing Apps.

The installed App runs through LaunchServices in standard mode, with no
external policy or capability manifest. The operator enabled Accessibility and
Screen Recording; production inspection reports both granted and verifies the
connection. Actual window captures subsequently passed. This establishes the
current service's access; the separate Settings repair gate below also exercises
the public grant/direct-capture command with these existing grants.

The opt-in [native Manager test](../internal/desktop/manager_native_darwin_test.go)
passed on this host on 2026-09-27. Three separate AppKit targets received Unicode
value changes and one commit each, independently confirmed after the responses.
All nine observations retained verified image/capture mappings. The foreground
sentinel retained focus and contents, consumed references were rejected, one
connection/session served the complete task, and closing it preserved the shared
service. Cold window discovery took 143 ms; each warm set/commit sequence plus
observations and fixture checks took 5.67–5.75 s. Individual Driver calls took
2.47–2.61 s, with follow-up observations taking 188–210 ms. These are single-run
fixture measurements, not a general desktop latency guarantee.

The pinned macOS structured projection contains actionable nodes and reports
`elements_complete:false`; passive result labels can be omitted. The Manager
gate therefore verifies the editable value in the observation and the commit in
independent AppKit state. Missing passive text remains unknown; a successful RPC
or retained input value alone does not establish that the button worked.

The application-level `TestNativeMacDesktopPrint` also passed. It uses the
production desktop constructor with a scripted model through the actual CLI,
Guard and Session. Three AppKit commits completed in 19.53 s; nine PNGs reached
subsequent model requests and exact durable-history replay passed. Native input
bodies stayed out of CLI progress, the armed sentinel recorded no activation
loss, and command cleanup preserved the shared service. An earlier Manager run
recorded focus loss and an earlier CLI attempt lost the foreground precondition
before actions; their causes were not attributed. The later complete passes do
not establish uninterrupted coexistence under arbitrary desktop activity.

The [cross-toolkit transfer gate](../internal/desktop/webkit_native_darwin_test.go)
passed with race detection on 2026-09-27. One production Manager connection and
session carried Unicode text through AppKit → WebKit → AppKit in three separate
synthetic processes, with one commit in each and nine verified window captures.
The WebKit fixture loads a local HTML form into a non-persistent data store;
its own page handlers report actual DOM value and commit state independently of
Driver accessibility read-back. The other stages use native widget state.
No model, browser automation, remote page or JavaScript input route is used.
The armed foreground sentinel retained focus and contents through cleanup,
and the shared service remained available. The three stages took 20.88 s after
discovery; the full gate, including compilation and setup, took 31.12 s.

This probe also exposed a real toolkit difference: `set_value` returned on the
WebKit field, but the DOM value and committed result did not match the requested
text. That attempt failed its independent postcondition. The pinned upstream
`set_value` implementation documents untrusted web-content AXValue writes.
The passing task instead uses `type_text` on an initially empty web input;
the Driver still reports `effect:unverifiable`, so success comes from the DOM
and commit checks, not the RPC status. Control selection requires both label
and role because WebKit exposes more than one element named `Task value`.
There is no automatic input retry or fallback in the production adapter.
This establishes two toolkits, not three distinct third-party applications,
Electron compatibility, browser profiles, actual-model reasoning or physical
input/IME coexistence. Those broader acceptance items remain open.

The corresponding actual-CLI `TestNativeMacWebKitPrint` gate also passed with
race detection on 2026-09-27. It runs two AppKit targets and the WebKit form
through the production desktop constructor, Guard, Loop, typed tools and Session,
using a scripted model. Three Unicode edits/commits completed in 24.55 s with
11 model requests and nine PNGs passed directly to later requests. Exact Session
replay retained the complete tool pairs and images, including the WebKit insert's
`effect:unverifiable`; independent DOM state confirmed its actual result.
No native input body appeared in text progress, the sentinel recorded no focus
loss, and CLI cleanup preserved the shared service. The existing AppKit-only CLI
gate passed in the same sequential run. These are native application-pipeline
checks, not actual-model visual reasoning or physical terminal input.

A separately opted-in [real-model gate](collaboration.md#explicit-real-model-desktop-gate)
is prepared but has not yet been run against a real provider. Its scripted
native harness passed with race detection in 23.23 s of Loop execution on
2026-09-27: 11 requests, nine images, zero Guard asks/scope refusals, independent
three-window postconditions, Session replay and focus/service cleanup passed.
The real path requires explicit provider/model/thinking selection and a fresh
artifact directory. It uses bounded requests and only synthetic desktop tools;
its test-only scope checks do not add a product app allowlist. Real-model task
success and model/network timing remain unverified until that opt-in actually
runs and passes, separately from the existing scripted CLI gates.

The separate [cold-launch gate](../internal/desktop/apps_native_darwin_test.go)
passed with race detection on 2026-09-27. It registers a unique synthetic AppKit
bundle in `~/Applications`, then uses real app discovery and a locally issued
app reference to launch it once through the production Manager. The native
response established the exact bundle ID/PID and returned two windows: the
named fixture and an untitled candidate. AICE returned both without automatically
observing either. The test explicitly selected the unique fixture title,
captured that window, changed its value to Unicode text and committed once.
The consumed app reference was rejected without another native launch.

Launch took 2.27 s and the full gate 16.12 s in this run. The Driver's reported
`self_activation_suppressed:true` was not the focus evidence: the armed AppKit
sentinel independently recorded zero activation losses through launch, input
and connection cleanup. The launched app kept producing state after Manager
close; shared-service inspection also remained usable. Test-owned cleanup then
requested fixture termination, unregistered and removed its temporary bundle.
An earlier test attempt incorrectly required a single candidate and stopped
before observation; the corrected gate preserves the specified multi-window
contract. This establishes ordinary synthetic AppKit cold launch, not behavior
of self-activating third-party apps, physical input/IME or other toolkits.

The [native cursor lifecycle gate](../internal/desktop/cursor_native_darwin_test.go)
passed with race detection on 2026-09-27 in 8.48 s without an external UI
observer. The production Manager's session was absent before discovery, present
with `cursor_visible:false` after observation, and reported visible after one
pixel click independently committed in the fixture. Closing the run removed the
session from the official `sessions list --json` operator view. The foreground
sentinel retained focus and contents, and shared-service inspection still worked.
This read-only test diagnostic is not a model tool or a new production poller.
The pinned source connects `cursor_visible` to the render collection's enabled,
on-screen, non-faded cursor state; it is stronger than an input RPC result, but
does not itself prove compositing, appearance or physical pointer independence.

A separate live inspection captured the Cua Driver's transparent host surface
through Computer Use. It visibly contained the blue, white-edged agent cursor
after the test click; a later capture after run close was empty. Capturing the
target application alone showed its changed result text but no overlay. This is
isolated overlay-surface appearance evidence, not a desktop composite, animation
sequence or proof that the disappearance was caused only by close rather than
idle fading. The combined manual-inspection runs failed their foreground
sentinel checks (two losses with PID 71102 foreground; one with ChatGPT PID 63540
foreground). The cause is not attributed, and those runs do not pass background
coexistence. A prior short inspection window expired before the observer bound;
the optional test handshake now permits binding before the single click. It
never replays input or changes cursor settings to obtain a screenshot. Overlay
placement over other windows, animation and multi-display/Space behavior remain
unverified; commands are in [collaboration](collaboration.md#computer-use-checks).

The separate `TestNativeMacInput` passed all four cases: semantic ASCII/Unicode
insertion, single-key input and select-all hotkey delivery. Independent AppKit
field-editor text/selection and post-response fixture frames established the
requested effects. The Driver reports select-all as `unverifiable`; the gate
passed because the field editor independently confirmed the complete selection.
Each sentinel retained focus and contents through input and connection cleanup.
The full four-case gate took 24.63 s, with input action/observation calls taking
1.23–1.35 s. Commands and fixture boundaries are in
[collaboration](collaboration.md#computer-use-checks).

`TestNativeMacPixelClick` passed both screenshot-coordinate click and resize
recovery cases. The fixture independently reports AppKit window/button geometry;
a 500×328-point window produced a 1000×656-pixel screenshot. The first click
completed in 1.90 s including observation. After the fixture resized, the Driver
returned `capture_frame_mismatch` with `effect:refused` in 312 ms including a
fresh observation, with no commit. A newly calculated point on the fresh image
then committed once in 2.01 s. The resized 900×378-point frame produced a
1600×672-pixel image, exercising Driver downscaling as well as Retina conversion.
Both cases preserved sentinel focus and contents
through cleanup. A returned `effect:unverifiable` on ordinary click was not used
as proof of success; the independent widget commit established it.
This does not establish double/right-click, overlay appearance, image crops or
multiple monitors with negative coordinates; gesture evidence follows below.

`TestNativeMacWindowMove` additionally passed with race detection on 2026-09-27.
After capturing the 500×328-point window at screen x=100, the fixture moved it
to x=−40 without changing its dimensions or content. The same screenshot-local
button point dispatched exactly one native click and produced one independent
widget commit. The negative-origin window remained there, a fresh bound image
was returned, and the sentinel retained focus and contents through cleanup.
Action plus observation took 2.03 s; the full gate took 8.84 s. This verifies
pure window translation with a partly off-screen window on one display. Unlike
resize, translation does not invalidate local image coordinates: the pinned
Driver resolves the window's current position when routing input. It does not
establish multi-display origins, display-scale transitions or cropped images.

The separate `TestNativeMacPointerButtons` gate **fails** for both background
pixel actions on the pinned macOS Driver. Its custom AppKit view has no AXPress
implementation or context menu and counts actual mouse-down/up events. Each
case dispatches exactly one native `click` RPC, starting with an active sentinel
and zero activation losses. Double-click delivers exactly two left-down/up pairs
with click count two, but the sentinel records one activation loss before focus
is restored. Right-click delivers two right-down/up pairs instead of one; that
case preserves sentinel focus. Both land at the requested center with the right
button/window and no unintended modifiers, while text and commit state remain
unchanged. Both Driver replies say `effect:unverifiable`. The counted run took
3.44 s and 3.19 s per action including observation; a preceding run reproduced
the same failures. Restored focus and non-error RPCs do not satisfy acceptance.

Source inspection of the [pinned revision](../internal/deps/cua/VENDOR.md)
provides plausible mechanisms: macOS `tools/click.rs` permits target activation
for a background raw left click, calls `prepare_background_pixel_click`, and
then restores the prior application/focus. The right-click path in
`input/mouse.rs` posts each down/up through `MousePostMode::Both`, which
unconditionally uses both SkyLight and public `post_to_pid`. The measured
single RPC rules out an AICE action retry; source inspection is not a trace of
which native transport delivered each event or which app caused the temporary
activation loss. Keep the exact-count and continuous-focus gates failing until
those behaviors are repaired. No custom Driver replacement, automatic replay or
implicit foreground fallback is introduced. This does not establish semantic
right-click/context-menu behavior or physical-input coexistence.

The separate `TestNativeMacGestures` gate passed background pixel scrolling on
an AppKit `NSScrollView`: its independently reported content offset changed from
0 to 60 points, with text, button count and slider unchanged. The action plus
observation took 3.08 s; the sentinel retained focus and contents. The Driver
reported `effect:unverifiable`, so the widget readback is the effect evidence.
The background drag case **fails**: 0.29.1 returned the code-only
`background_unavailable` response, with the `NSSlider` still at zero and no focus
loss. The pinned `drag.rs` rejects this route before target resolution or input.
The gate retains its requested movement postcondition; refusal is not a pass.

An initial `TestNativeMacForegroundDrag` run completed in an explicitly opted-in
`foreground_allowed` mode. Its first background request was refused; the test
then rejected reuse of that consumed observation and used the returned fresh
image with `foreground_action_available:drag` for one explicit foreground
request. The slider moved from 0 to 92.7, while text, button count and scroll
position remained unchanged. The sequence recorded one foreground-sentinel
activation loss and restored its focus before cleanup. This is expected
foreground interaction, not background coexistence, and does not make the
background drag gate pass. No saved control-mode preference is changed by the
test. A later probe additionally verified zero focus loss before the explicit
foreground dispatch, but failed afterward: the slider remained at zero and the
sentinel was inactive with ChatGPT foreground. A preceding regression attempt
also lost focus to iTerm2, then failed to establish the next sentinel's initial
focus before input. These observations do not attribute who changed focus;
foreground-drag repeatability remains unaccepted. They reinforce that
`effect:unverifiable` must retain uncertain delivery rather than report success
or trigger automatic replay. Commands and remaining geometry/physical-input
limits are in [collaboration](collaboration.md#computer-use-checks).

The two [native cancellation gates](../internal/desktop/cancel_native_darwin_test.go)
passed with race detection on 2026-09-27. `TestNativeMacCancelWait` closes a run
after a completed native condition poll while a concurrent click attempts to
acquire execution. Close took 2.64 ms; both calls settled as cancelled, with
zero native click calls or widget commits. The session ended once, its references
were invalidated, and a new run on the same connection required fresh discovery
and observation before one explicitly requested click succeeded.

`TestNativeMacCancelDispatchedClick` independently observes one widget commit
while the native click RPC is still pending, then closes the run. Close took
1.003 s and retained `dispatched:true` / `outcome:unknown`. The old task
connection was retired; recovery used fresh discovery and capture on a second
connection without repeating the click. Exactly one native click call and one
widget commit were observed, and old references were rejected. The two runs
started two sessions but made only one explicit `end_session` call, for the
recovery session; the cancelled transport had already been retired. A separate
read-only inspection confirmed the shared service remained usable. These are
single-run timing samples and Manager lifecycle evidence, not proof of native
TUI Stop during mutations, interrupted gesture cleanup, physical-input coexistence
or every resource's cleanup inside Cua.

The separate [native Settings Stop gate](../internal/app/desktop_stop_native_darwin_test.go)
also passed with race detection on 2026-09-27. A scripted model discovers the
exact synthetic AppKit window, receives its real capture, then issues a native
condition wait. Through the actual CLI/Bubble Tea UI, Esc closes Settings without
ending that wait; reopening Settings and invoking its F6 Stop cancels it. The
cancelled response appeared in 400 ms, polling had started, run cancellation
preceded its one cleanup, and no fourth model request or widget change occurred.
Reopened Session history retained all three complete tool pairs and the capture;
the cancelled wait was an error result. Saved enable/control-mode preferences
and the shared service were preserved. This 11.37 s gate exercises terminal
input through pipes and has no foreground sentinel; it does not establish
physical keys, IME, foreground coexistence or stopping a native mutation.

`TestNativeMacDesktopStopMutationTUI` extends the actual CLI gate to a click
that independently committed before its native response returned. Only the
scripted model's next decision is held while Settings opens; native input and
responses are not delayed or replaced. After the AppKit fixture reports exactly
one commit, Settings F6 cancels the run. The result must be dispatched and
unknown, with no follow-up observation; a completed response cannot satisfy this
in-flight gate. It passed with race detection in 12.31 s, with cancellation
visible in 1.11 s. Session replay retained the exact unknown dispatch result,
three complete tool pairs and the initial image. There was one action, one
cancelled binding cleanup, no fourth model request and no replayed commit.
Saved preferences and the shared service remained usable. This covers native
click cancellation through piped terminal input, not interrupted drag/key-release
cleanup, physical Stop keys or foreground/IME coexistence.

These gates require the installed authorized service and an available desktop;
their preparation installs nothing and requests no grants. They use no real
model and do not prove physical keyboard, IME, remaining pixel routes, overlay
compositing/animation or heterogeneous application behavior. First-time Settings installation and physical interaction
with system authorization dialogs remain separate from the operator-performed
upgrade and grants above.

The fixed source's platform matrix documents limitations for raw Wayland
background input and toolkit-specific paths. Structured refusal is not proof
that a promised action is supported. AICE must preserve `background_only`,
report unsupported routes and obtain a product decision if an upstream limit
prevents the agreed acceptance. Windows/Linux remain in scope. Windows native
desktop validation is still missing. Linux arm64 has separate headless installation
and isolated X11 capability evidence, with the exact scope and commands in
[collaboration](collaboration.md#computer-use-checks).

Windows Settings now has a bounded read-only inspection path for an already
verified private installation and the existing `\\.\pipe\cua-driver` service.
Before and after the two-tool MCP exchange it checks the public standard-mode
status, the pipe server PID, the corresponding executable file, user SID,
same Windows login session and process creation time. The local identity probe
uses identification-only SQOS; it sends no protocol request and grants no
service ownership. These API choices follow Microsoft's
[pipe-server identity](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getnamedpipeserverprocessid)
and [CreateFile SQOS](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew)
contracts. AICE never starts, elevates, reconfigures or stops the service in this path.

The Windows report separates the Driver's integrity RID/name, its reported
UIA/PostMessage prerequisites, the service process's actual UIAccess token bit,
and its login session. A failed integrity lookup remains Unavailable; a failed
UIAccess query remains Unknown. Session 0 is shown as a services session, and a
nonzero session is not evidence of an unlocked/input-ready desktop. The fixed
Driver reports UIA and PostMessage as constants, so those fields never establish
target capability. Status does not inspect the separate UIAccess helper or grant
access to higher-integrity applications or UAC surfaces. Windows setup and actions
remain unavailable even when the status connection is verified.

The Windows status schemas were reviewed from the fixed source, not a native
metadata export. Synthetic admission tests cover external restrictions, foreign
or changed identities, PID reuse, malformed/contradictory facts and cancellation;
the actual CLI/Bubble Tea test renders these synthetic Windows facts. The native
named-pipe tests and opt-in existing-service inspection test compile for Windows
but have not executed here. No Windows native status or desktop acceptance is
claimed; commands and boundaries are in [collaboration](collaboration.md#computer-use-checks).
The separate [Windows action source review](#windows-action-admission-gaps)
records launch identity and focus issues that must be resolved before admitting
its action runtime.

The native Linux 0.29.1 metadata export contains all 15 tool names currently used
by AICE, but 11 input schemas differ from the macOS pin. Only `get_config`,
`list_apps`, `start_session` and `end_session` match. In particular Linux's
`check_permissions` takes no `prompt` field, and window/element/action parameter
contracts differ. Its permission response reports X11, Wayland and AT-SPI facts
without the macOS daemon attribution fields. Consequently platform integration
requires reviewed typed adapters and process-identity verification; admitting
Linux by bypassing the current macOS schema check would be incorrect.

Linux Settings now inspects an existing verified installation at the pinned
`~/.cache/cua-driver/cua-driver.sock` endpoint. It checks standard authorization
mode and policy, ties the reported PID to the Unix socket's `SO_PEERCRED` user
and PID, and checks `/proc/<pid>/exe` against the verified executable path and
inode before and after the read-only MCP exchange. The separate status client
admits only the two reviewed Linux status tools. Its five-second inspection
deadline covers the probes and handshake; it neither starts nor repairs a
service, creates a desktop session, observes a window, or captures.

The status projection separates X11 connectivity, AT-SPI bus ownership, Wayland
environment presence, backend enablement and the XSendEvent prerequisite. Unknown
fields stay unknown, and Wayland environment presence is not compositor or input
verification. No D-Bus address or upstream free text is projected. A verified
service connection alone does not establish capture or target-input readiness.
An absent shared service is compatible with an active owned tool process; the
panel shows that cached instance connection separately, with unknown shared
display facts. Without an active connection it reports that connection happens
on first use. Refresh still starts nothing. The isolated native Settings CLI
flow passed as described below; physical terminal/IME and other compositor
acceptance remain incomplete. The Linux arm64 headless
fixture passed two inspections of its own service and correctly reported absent
display/bus capabilities while leaving that service alive between checks.

The opt-in Linux background probe uses a private Xvfb display, Openbox and AT-SPI
bus, three GTK fixture processes, and a fourth foreground fixture receiving
continuous XTest core keyboard events. It uses the actual pinned stdio MCP
client and the reviewed production Linux schema inventory. Independent fixture state
checks semantic Unicode writes and single button commits; PNG bytes and reported
dimensions are checked on each observation. This is upstream native capability
evidence, separate from the Manager acceptance below. Three GTK copies do not establish other toolkit,
physical keyboard, IME, pixel input, GPU, overlay or Wayland compatibility.
On 2026-09-26 the arm64 probe passed in 8.81 seconds: all three commits matched
independent application state, stale tokens were rejected, and the sentinel
retained all 69 injected core keystrokes with zero focus loss. The separate
negative control detected deliberately misdirected input and retained the focus
loss after restoration. Per-target set/click plus three captures took about
1.5–2.7 seconds; those aggregates are not a model/Guard/native-action timing
breakdown or a guarantee for other applications.

The production Manager test subsequently passed both owned-stdio and shared-service
paths in the same isolated arm64 environment. Each made three Unicode edits and
single button commits with nine verified screenshot mappings, one connection,
one session and no replay of a consumed AICE reference. Independent fixture state
confirmed each result. The foreground sentinel retained all 69 core keys in each
mode, with zero focus loss. Closing the Manager reaped owned MCP children
and left the shared service available; the private path created no shared socket.
Cold discovery took approximately 157 ms and 91 ms in that run; each full
three-target case took about 8.8 seconds. These are synthetic X11 Manager
measurements, not full model/tool/Guard/Session acceptance. Linux-only rejection
tests also confirm that external restrictions, unknown status and foreign peers
do not trigger an owned-runtime fallback.

On 2026-09-27 the same native Manager gate passed again with phase timing enabled
(0.29.1, Linux arm64, isolated Xvfb/Openbox/GTK, no model or provider network).
Owned stdio and shared service each retained all 70 concurrent core keys with
zero focus loss and passed session/process cleanup. Cold discovery took about
135 ms and 83 ms respectively. Across the twelve warm actions, final observations
took 7.2–28.6 ms; the six click RPCs took 1.436–1.480 seconds. The first value-setting
RPC in each mode took 2.8–7.5 ms, while later value-setting RPCs took 1.150–1.159
seconds. Queue admission stayed below 5 microseconds in this uncontended run.
This locates most measured action latency within the Driver RPC boundary, but
does not distinguish transport, upstream waits or native input work inside it.
These single-run ranges are not percentiles, an end-to-end performance result,
or evidence for macOS or another desktop environment.

The separate native print test passed on Linux arm64 in the isolated X11 fixture.
It installs the checksum-pinned archive into a temporary HOME through the real
installer, then uses ordinary enabled configuration, the actual command, Guard,
typed tools and production Manager with a scripted model. No `--yolo` or desktop
backend substitution is used. Three exact-window Unicode edits and commits
completed in about 7.1 seconds; independent GTK state confirmed all three.
Nine valid PNG tool results reached subsequent model requests and replayed
unchanged from Session JSONL, with stable message parents and paired calls.
All 70 concurrent core keys remained in the sentinel, with zero focus loss;
the owned Driver was gone after command completion. Text progress omitted input
contents. This verifies native execution and image transport; the script does
not interpret pixels, and setup UI, actual-model reasoning and physical input
remain separate acceptance work.

The native Settings CLI gate also passed on Linux arm64 in the isolated X11
fixture on 2026-09-27. It drove the real `/desktop` modal with production desktop
construction, private installation, native setup and the Settings writer; only
the pinned archive's HTTP delivery used a local file. Confirmation preceded the
single installation. Cancelling the default window choice retained that verified
installation, left enable false and capture unrecorded, and reaped the temporary
Driver. Retrying reused the same installation, explicitly selected the synthetic
window, verified capture, saved enable and displayed historical capture status.
Neither attempt requested a model response or created a Session. Final command
cleanup left no private Driver process. The 3.68-second fixture run retained all
28 concurrent core keys with zero focus loss. This is native setup backend plus
CLI/Bubble Tea evidence; it does not prove physical terminal/IME interaction,
public-network downloads, other compositors or another platform's authorization.

The Linux success response omits `screenshot_frame_valid`; the same fixed source
sets it to false when a capture error occurs. The native probe confirms the
omission alongside a capture ID and matching PNG dimensions. The admitted X11
adapter accepts that omission only without a domain or screenshot error and
still requires exact target identity, a capture ID and actual matching image
dimensions. Explicit false or conflicting evidence prevents pixel binding.
The macOS requirement for explicit true is unchanged. The isolated Linux GTK
input gate additionally exercises a screenshot-bound button click and rejection
of the old capture after a 420×180 to 900×350 resize. Its coordinates come from
the synthetic widget geometry; this is coordinate/lifecycle evidence, not model
visual grounding, multi-monitor or Retina acceptance.

### Windows action admission gaps

Review of the pinned 0.29.1 Windows implementation on 2026-09-27 found that the
macOS/Linux launch adapter cannot be reused by changing only the platform gate.
This is source evidence, not a native Windows acceptance result. Windows actions
remain unavailable; the read-only status connection does not admit these tools.

The fixed [`LaunchAppTool`](https://github.com/trycua/cua/blob/7a8f66ad04e62fccb18cca9965f2964fcaee124e/libs/cua-driver/rust/crates/platform-windows/src/tools/impl_.rs#L2034)
uses discovered `launch_path` commands or packaged-app AUMIDs. Plain desktop
launch responses have a null `bundle_id`; nested windows omit `pid`. Packaged
apps can return an ApplicationFrameHost PID instead of the activated app PID.
When the initial process has no window, a separate fallback can replace the
returned PID with a descendant **or a name-related process**. Neither copying
the top-level PID into nested windows nor matching the response's display name
establishes the requested application's identity.

The [`related_processes`](https://github.com/trycua/cua/blob/7a8f66ad04e62fccb18cca9965f2964fcaee124e/libs/cua-driver/rust/crates/platform-windows/src/win32/apps.rs#L84)
helper strips version suffixes and includes unrelated processes with the same
remaining executable name. The launch fallback builds a vector in that order,
then removes candidates from its end. A local Rust diagnostic compiled the exact
pure helper functions and candidate-queue construction, replacing only process
enumeration with this fixture:

| PID | Parent | Executable | Relationship to launch PID 42 |
| --- | --- | --- | --- |
| 42 | 1 | `gimp-3.exe` | Launched process |
| 43 | 42 | `gimp-3.2.exe` | Actual child |
| 99 | 1 | `gimp-3.3.exe` | Unrelated process |

It produced descendants `[42, 43]`, related candidates `[42, 43, 99]`, and first
fallback candidate `99`. If that candidate has a window, the source returns it
before checking child 43. This diagnostic executed no Windows or GUI APIs;
it demonstrates the selection algorithm, not an observed misdirected native
action. The reviewed helper file was also byte-compared with the fixed official
source. Native acceptance must cover an existing unrelated same-name process,
launcher handoff and packaged host windows, and establish exact ownership before
issuing a target reference. A launch response alone is insufficient evidence.

The ordinary launch route also schedules best-effort focus restoration **after**
activation. Its `active:false` field is a constant, not a focus measurement.
`start_minimized` adds foreground locking and window minimization, with another
process-family/name heuristic; it is not an accepted replacement for the agreed
background launch behavior. Keep continuous focus/input sentinel acceptance,
including after the response, and obtain a product decision before weakening it.

Input refusals need their own review too: `finish_pixel_uia_attempt` reports
`background_unavailable` with `effect:unverifiable` after a UIA timeout or provider
failure, while its advice suggests foreground input. That advice cannot authorize
replay. Preserve unknown/partial effects and require reviewed evidence of a
pre-input refusal before offering foreground continuation. The existing macOS
classifier must not be enabled for Windows based on a matching error code.

### Linux input acceptance failures

The opt-in `TestNativeLinuxInput` is an acceptance gate, not an expected-failure
test. Its full run currently fails with the fixed Linux arm64 0.29.1 Driver:

- GTK `type_text` with mixed ASCII/CJK text and a check mark returns a non-error
  `effect: unverifiable`, but independent widget state contains only a prefix.
  The 17-byte, 11-character request produced 10 bytes and 8 characters after
  waiting for fixture frames published after the Driver response. The separate
  12-character ASCII case passed. All six cases retained the foreground
  sentinel's concurrent input without focus loss.
  ASCII insertion and Unicode `set_value` are separate routes; their success
  does not establish Unicode insertion. The fixed source's AT-SPI insert helper
  passes a character count as the insertion length. The official
  [AT-SPI contract](https://gnome.pages.gitlab.gnome.org/at-spi2-core/libatspi/method.EditableText.insert_text.html)
  requires a byte count for the UTF-8 text, while the insertion position remains
  a character offset. The [ATK contract](https://docs.gtk.org/atk/method.EditableText.insert_text.html)
  also specifies bytes. These are different units for this failing request.
- GTK `key` and `hotkey` return `background_unavailable` in the isolated
  Xvfb fixture. The fixed source requires a real independent keyboard route for
  GTK; this container has no `/dev/uinput` access. This is an unavailable route
  in the tested environment, not evidence that those inputs work on a physical
  Linux desktop. Do not mount the user's input devices to make the fixture pass.
- GTK pixel `scroll` and `drag` also return `background_unavailable` in this
  environment. On 2026-09-27, separate cases used a real `GtkScrolledWindow` and
  `GtkScale`, with screenshot coordinates derived from fixture geometry. Scroll
  offset and slider value remained zero in independent application state sampled
  after the replies. Each case retained all eight concurrent core keys in the
  foreground sentinel with zero focus loss. This is successful refusal handling,
  not successful scrolling or dragging, and it does not validate gesture geometry
  after dispatch. The pinned implementation's `unavailable_gtk_pointer_background`
  gate requires an available independent pointer route; its
  `real_pointer_input_available` check returns false without `/dev/uinput` access.
  The fixture intentionally supplies no host input devices. Passing GTK button
  clicks can use a semantic route and do not prove general pointer delivery.

The complete eight-case native run on 2026-09-27 took 20.51 seconds: ASCII
insertion, button click and resize/refusal/re-observation passed; Unicode
insertion, key, hotkey, pixel scroll and drag failed. Every case retained its
concurrent foreground input without focus loss. The tests retain the requested
postconditions and fail when input does not land.
AICE preserves the native refusal/unverifiable result and does not replay it or
switch to foreground. The Unicode insertion discrepancy is unresolved; do not
claim complete Linux input support, substitute `set_value` for insertion, or
change the pinned artifact silently. A repaired/reviewed Driver and an isolated
environment with supported independent keyboard/pointer routes need fresh acceptance.

An isolated arm64 Debian/GTK control experiment on 2026-09-27 confirmed the
length mismatch independently of Cua. Against the same synthetic entry fixture,
AT-SPI `InsertText(0, "Native 中文 ✓", 11)` returned success but produced the
same 10-byte, 8-character prefix. Clearing the entry and passing length 17
instead produced the exact 17-byte, 11-character text. A 12-byte ASCII control
also matched exactly. Readback waited for three fixture frames after each reply.
This establishes the length-unit defect in the reviewed insertion path; it is
not a patched-Driver test, background-input acceptance or an alternative AICE
backend. The experiment used a private display/bus and no host input devices.

An earlier official-source review on 2026-09-27 found no repaired insertion path
in the reviewed nightly or main snapshot. Nightly
[`0.29.2-nightly.20260926.36217989449`](https://github.com/trycua/cua/releases/tag/nightly-cua-driver-rs-v0.29.2-nightly.20260926.36217989449)
resolves to commit `7ee9b37edc4ebc5f7f606682ae2699d1baa5d397`;
main was `5b3d48dfda23bde15ae1f2c150940defbdc64c21`. Their
[`native.rs`](https://github.com/trycua/cua/blob/5b3d48dfda23bde15ae1f2c150940defbdc64c21/libs/cua-driver/rust/crates/platform-linux/src/atspi/native.rs#L2721)
files were byte-for-byte identical and still passed `text.chars().count()` to
`EditableText.InsertText`. The `set_value` insertion fallback also used character
count; the passing GTK `set_value` test exercises `SetTextContents`, so it does
not validate that fallback. This is a dated source review, not execution of the
nightly or a guarantee about later releases. No release pin has been changed.

### Subsequent 0.30.1 source review

A later release query on 2026-09-27 found the stable
[0.30.1 release](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.30.1),
published on 2026-09-26 at 19:45:40 UTC, at commit
`039783f9221a08c0daf9cda65a460fc4f346fa6e`. Upstream uses GitHub's prerelease
label for monorepo release routing; its notes identify plain SemVer releases as
stable. The review compared this commit directly with the pinned 0.29.1 commit,
including intervening 0.30.0 changes. The 0.30.1 notes themselves describe a
Windows isolated-browser de-elevation fix.

The macOS `input/mouse.rs` and `tools/drag.rs` files are byte-for-byte unchanged
from 0.29.1. The click route adds hardware-pointer/HID delivery for explicit
foreground window clicks and refuses background pixel clicks for detected Tk
targets. The AppKit background route still permits target activation and
restores the prior foreground app; its mouse helper still has the dual-post
right-click path. These source changes do not establish a repair for the
observed background double-click focus loss, duplicate right-click events or
drag failures. See the fixed-commit
[click route](https://github.com/trycua/cua/blob/039783f9221a08c0daf9cda65a460fc4f346fa6e/libs/cua-driver/rust/crates/platform-macos/src/tools/pixel_route.rs)
and [input implementation](https://github.com/trycua/cua/blob/039783f9221a08c0daf9cda65a460fc4f346fa6e/libs/cua-driver/rust/crates/platform-macos/src/input/mouse.rs).

The Linux
[insertion path](https://github.com/trycua/cua/blob/039783f9221a08c0daf9cda65a460fc4f346fa6e/libs/cua-driver/rust/crates/platform-linux/src/atspi/native.rs#L2735)
still passes `text.chars().count()` to `EditableText.InsertText`. Its reviewed
diff changes pixel hit-test filtering, not this length calculation. This is
source evidence only: 0.30.1 has not been installed, schema-admitted or run
through native acceptance here. The installed App and AICE artifact/schema pins
remain 0.29.1; existing failed native gates remain unresolved.

### Linux launch acceptance failure

`TestNativeLinuxLaunch` exercises a temporary XDG desktop entry through the
production Manager in the isolated arm64 X11/GTK fixture. On 2026-09-27 the
pinned 0.29.1 Driver launched it once, returned the exact PID/window with an
immediate screenshot, rejected reuse of the consumed AICE app reference, and
allowed a Unicode value change and button commit. Independent fixture state
confirmed the task and continued advancing after Manager close: the app's
lifetime is separate from AICE's connection. Launch plus observation took about
128 ms; the full fixture case took 3.67 seconds, with no model call.

The full gate nevertheless **failed**: the foreground sentinel lost activation
once and remained inactive after launch, while Cua reported `active:false`.
All five core keys sent in that interval were retained, so this run establishes
focus interference, not observed keyboard misdelivery. Returning a process and
window successfully does not satisfy background coexistence. The fixed Linux
launch helper spawns the application but does not prevent its mapped window
from activating; the returned `active` field is a constant false, not a measured
focus fact. AICE does not restore focus or replay launch to disguise this result.

Keep this failure separate from successful inputs into already open background
windows. No repaired Driver or product exception has been accepted. The gate
must preserve focus/input requirements and be rerun after a reviewed fix;
changing a window-manager setting solely to make this case pass would not prove
the existing launch route. Other window managers, D-Bus handoffs and Windows
launch behavior require their own native evidence; macOS AppKit evidence is
recorded separately above.

### Shared runtime and application verification

The SDK closes both stdio stream sides; both share one idempotent process owner
so cleanup closes and reaps the child only once. Normal proxy exit returns success;
hung-child cleanup and nonzero exit reporting retain their existing behavior.

The C0 local schema probe used an isolated HOME and disabled telemetry. Its
sandboxed invocation failed during AppKit pasteboard initialization; the
read-only schema export succeeded with normal GUI access. The opt-in inventory
test repeats the metadata comparison for the 15 used tools. Neither invocation
read a window or executed a desktop action. No OS permission was requested,
App installed, user application controlled, or paid model called.

Default tests use raw fake MCP peers and synthetic bytes. Native tests must
be explicitly opted into and use synthetic applications and data. Cross-builds
and upstream test claims do not replace local native evidence.

Application tests also execute the real Print command and interactive Loop with
a scripted model and fake desktop backend. They cover disabled/enabled tool
publication, binding cleanup, stale-context denial, Web recomposition, failed
preference saves, and partial action/image retention in Session records. They
never enumerate or control the user's desktop.

The default Settings CLI/TUI test traverses search, setup, disclosure,
confirmation and persisted enable with fake installation/authorization. The
separate native `TestNativeMacDesktopSetupTUI` passed with the race detector on
2026-09-27 in 10.47 s after asynchronous Settings status loading was integrated.
It cancels before external work, retries and confirms repair, reuses the verified
installed App with downloads disabled, then runs the real public grant/capture
flow and checks the displayed permission/capture facts. Enable is persisted only
in temporary configuration; no model request or Session is created, and command
cleanup preserves the shared service. This is repair with existing grants, not
first installation or physical interaction with a system permission dialog.
Its explicit opt-in is documented in
[collaboration](collaboration.md#computer-use-checks).
Default application tests cover concurrent preparation and
writer rejection, peer-setting preservation, save failure after authorization,
cancelled grants, preference-only retry and frozen download policy. Renderer
tests cover 80×24, 120×40 and 32×16 disclosures and visible partial results. These
do not substitute for native OS UI or IME acceptance.
The CLI/TUI flow also opens `/desktop` during a synthetic blocked model run,
checks that Esc leaves it running, and cancels through Settings F6. Its fake
desktop binding verifies cancellation precedes cleanup and enable remains saved.
Renderer tests cover the mouse button, information-page Stop and the early
preparation interval before the controller publishes cancellation. The CLI flow
also re-enables after cancellation, verifies no automatic model request, and
explicitly continues and stops a second run. Unit tests cover proposal staleness,
repeat use, no setup history writes, draft/attachment retention and rejection.
The separate `TestDesktopActivityTUI` drives the real command, Loop and typed
tools with a synthetic discovery/observation/wait backend. It checks the app
label, collapsed input, Planning and Settings Stop through Bubble Tea. Renderer
tests cover model waits, unknown/setup outcomes, narrow/CJK/untrusted labels,
new-run reset and history projection. These checks do not read desktop content.
The same Settings CLI flow opens the status details with synthetic permission facts.
Default inspection tests reject foreign/changing identities, retain Missing and
Unknown separately, bound cancellation, and assert that only the two read-only
inspection calls execute. Screenshot tests record actual success and failure,
while semantic-only observations leave capture Not checked. The opt-in absent
socket test also exercises the public inspection API without starting a daemon.

Manager tests use synthetic windows and PNGs to verify single dispatch,
post-observation failure, cancellation, per-run cleanup, reconnection,
cross-run snapshot invalidation, malformed-image semantic fallback and exact
2100-to-2000-pixel coordinate conversion. These are offline lifecycle checks,
not native background-input or overlay acceptance.
Foreground tests cover explicit background-first dispatch, frozen-mode denial,
unchanged action content, fresh element tokens, opportunity expiry, transport and
post-observation failures, and conservative refusal classification. They do not
establish native focus restoration or actual foreground delivery.
Pixel scroll/type/drag tests check exact session/window routing, image-coordinate
rescaling, absence of unsupported capture arguments, missing/invalid image
rejection, bounded gestures, explicit foreground choice and fresh-image gating.
These remain synthetic tests; no user window is captured or controlled.
Occupancy tests use temporary files and two managers, plus an independent helper
process whose forced exit demonstrates kernel lock release on the tested host.
They cover cancellation, connection recovery, shared local bindings, setup
contention, and late completion after the run-cleanup deadline. macOS native
GUI delivery remains separate; Windows/Linux lock implementations are not proof
of native desktop acceptance on those platforms.

The explicit macOS installer API stages and verifies the signed App, reuses
a compatible existing installation, preserves conflicting files and respects
the current helper-download policy. Cold enabled runs use its read-only reuse
path; Settings uses the explicit install path after confirmation.
See [installation](installation.md#computer-use-helper-integration-in-progress).
Its opt-in artifact test passed on macOS using the pinned local archive. This
checks extraction, native signature/Gatekeeper verification and exclusive
publication in temporary directories; it never installs into `/Applications`,
starts a service, requests OS permissions or reads the desktop.
