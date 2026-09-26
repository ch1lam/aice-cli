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
The feature's enabled preference remains separate. Refresh occurs on panel reads
and manual refresh, not on streaming tokens or hover, and cancellation ends the
read without publishing its snapshot. Status reads hold no Settings write
reservation and never advance the configuration revision.
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
triggers another launch. Launch itself uses Cua's background-launch behavior;
native focus-side-effect acceptance remains open.
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
native geometry-change and foreground-delivery acceptance remains outstanding.
Both point axes must be explicit numbers; omitted axes never become zero.

Tool adapters serialize bounded domain facts as JSON text and append the actual
image as an existing image content part. They preserve partial/unknown dispatch
facts and any follow-up observation even when a later error occurs, marking the
result as an error without replacing it with a generic Go error. Images and
originals continue through the existing provider projection and Session JSONL.

Text `--print` progress reports desktop tool names, status and elapsed time;
it omits argument details so input text and observation queries are not copied
to stderr. Full calls and results remain in the Session. Explicit
`--output-format json` retains the existing [NDJSON event contract](contracts.md#print-ndjson-events),
including arguments and bounded result text; it is transcript output and can
contain window contents, not a content-free diagnostic stream.

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
| macOS 0.29.1 universal artifact | Download hash matches fixed release manifest; temporary extraction/exclusive publication preserves signature, signing identity and Gatekeeper acceptance; CLI/schema inspected; Settings setup exercised through CLI/Bubble Tea with fake native operations | Signed installed service, system authorization, persistent MCP handshake against that service, synthetic multi-app task, background focus/input sentinel, native overlay, cancellation and cold/warm measurements |
| Windows amd64/arm64 | Downloaded archives and selected executable hashes verified; private installer with Authenticode checks implemented; synthetic extraction/reuse/cancellation tests and cross-compilation pass; static imports inspected | Native installation/signature trust and exclusive publication; runtime/service admission, interactive-session/UIAccess detection, native UI/input/lifecycle tests |
| Linux arm64 | Private installation/reuse and read-only headless inspection passed in an isolated Debian 13 container; production Manager and selected-window setup passed owned stdio and verified shared-service X11/GTK checks; scripted-model native print/Guard/tool/Session flow passed; actual Settings CLI flow passed with synthetic native operations | Fully native Settings installation workflow and actual-model tasks; native launch, pixel/keyboard/drag routes, foreground assistance, overlay, other toolkits, real compositor/Wayland and physical-input/IME checks |
| Linux amd64 | Downloaded archive and selected executable hashes verified; synthetic installer tests and cross-compilation pass; ELF library dependencies inspected | Native installation/dynamic loading and exclusive publication; runtime/service admission, AT-SPI/display detection, compositor-specific input/capture/overlay tests |

On 2026-09-26 the native host had a signed, Gatekeeper-accepted CuaDriver
**0.7.0** in `/Applications`, distinct from the reviewed **0.29.1** artifact.
The pinned public status command reported no daemon at the default endpoint.
The existing App was preserved; using it does not satisfy pinned admission.
The opt-in native manager test was run and refused this version before creating
fixtures or connecting to Cua, through the production installation verifier.
An upgrade of this pre-existing global installation requires an explicit
operator decision rather than an automatic overwrite.

The opt-in [native manager test](../internal/desktop/manager_native_darwin_test.go)
and its synthetic AppKit fixture are now available; invocation and boundaries
are in [collaboration](collaboration.md#computer-use-checks). The fixture compiles
on this host. Its lifecycle probe created visible windows but could not activate
the sentinel: macOS `loginwindow` was foreground. This is a failed environment
precondition, not a Cua background-input result. Owned fixture processes were
confirmed gone after cleanup. The full native manager task has not passed;
it still needs the compatible installed service, grants and an available desktop.

The fixed source's platform matrix documents limitations for raw Wayland
background input and toolkit-specific paths. Structured refusal is not proof
that a promised action is supported. AICE must preserve `background_only`,
report unsupported routes and obtain a product decision if an upstream limit
prevents the agreed acceptance. Windows/Linux remain in scope. Windows native
desktop validation is still missing. Linux arm64 has separate headless installation
and isolated X11 capability evidence, with the exact scope and commands in
[collaboration](collaboration.md#computer-use-checks).

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
on first use. Refresh still starts nothing. Native interactive Settings and
compositor-specific acceptance remain incomplete. The Linux arm64 headless
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

The Linux success response omits `screenshot_frame_valid`; the same fixed source
sets it to false when a capture error occurs. The native probe confirms the
omission alongside a capture ID and matching PNG dimensions. The admitted X11
adapter accepts that omission only without a domain or screenshot error and
still requires exact target identity, a capture ID and actual matching image
dimensions. Explicit false or conflicting evidence prevents pixel binding.
The macOS requirement for explicit true is unchanged. Native pixel-action and
geometry-change acceptance remains open despite this capture-binding evidence.

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

The macOS Settings CLI/TUI test traverses search, setup, disclosure, confirmation
and persisted enable with fake installation/authorization. It verifies that setup
creates no Session. Default application tests cover concurrent preparation and
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
