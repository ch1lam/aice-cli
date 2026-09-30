# Computer Use integration

Computer Use is being integrated with Cua Driver. The current implementation
contains pinned distribution metadata, a persistent generic MCP connection, User-only
configuration fields, and application-owned desktop run bindings. When enabled
in user configuration, Print and interactive main runs make `managed:cua`
available through `tool_search`, the existing Loop and identity-bound Guard.
Native operation schemas enter the next model round only after discovery.
The builtin `computer-use` Skill supplies version-matched guidance on demand.
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
`internal/desktop` owns pinned Cua admission, native session lifecycle and
control-mode enforcement, and consumes
`internal/mcpclient` for protocol, framing, cancellation and exact child ownership.
The application owns configuration publication and run bindings. The existing
Agent Loop, Guard, media pipeline and Session remain the execution boundaries.

The generic client uses official Go MCP SDK v1.6.1. Cua connections pin `initialize` to
`2025-06-18`, check the returned protocol and Cua identity/version, then discover
tools once within 16 pages, 256 entries and 4 MiB of catalog pages. Incomplete
discovery is rejected. Before any `tools/call`, the desktop admission layer compares the
complete input schemas of all 15 used tools against the corresponding macOS or Linux
[reviewed 0.30.4 inventory](../internal/desktop/schema/README.md). Only JSON
object ordering and whitespace are ignored; missing tools or changed fields,
required parameters, defaults, enums, bounds and target alternatives reject the
connection. Additional upstream tools remain unavailable to the private client.
A version/platform update requires reviewing both its schema pin and adapters.
This validates the advertised contract, not actual native behavior. The generic
client preserves exact structured JSON numbers. Production operations use the
generic result mapper and retained-result readback. The old `desktop_*` model
adapters have been removed; old Session presentation is retained.
Configured MCP admission reserves the managed native endpoint; full
platform/model acceptance remains incomplete.
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
not stop a shared service. The generic client allows 250 ms for pipe closure and
process exit, then kills only that exact child and waits up to two seconds for
cleanup/reaping, reporting failure if that bound expires. Pipe closure is included
because a Windows pipe can wait for in-flight I/O. Normal and forced cleanup,
including a synthetic blocked Close, have deterministic regression coverage.
The shared-service path requires an explicit endpoint.
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
image support, and capture verification. The retained capture status comes from
explicit setup verification and carries a timestamp. Ordinary managed task
results pass through generic MCP without updating this status. It is historical,
not a guarantee for the next capture; upstream
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

### Managed MCP boundary

The admitted connection implements the same `Tools`, `ToolGeneration`,
`CallChecked` and server-information capabilities used by the application MCP
catalog. It retains complete descriptors, ordered content, structured JSON and
execution state. Initialization information is available through
`mcp_server_info` after admission without another native call. Catalog changes,
closed transports and cancellation invalidate old executable versions; the
caller must explicitly discover again. No operation is replayed on reconnection.

The run-owned backend exposes 11 upstream operations: `list_apps`,
`list_windows`, `get_window_state`, `launch_app`, `click`, `drag`, `type_text`,
`set_value`, `press_key`, `hotkey` and `scroll`. It retains their pinned input
schemas and descriptions, removing only the public `session` argument because
AICE injects its run-owned native session. The other four reviewed operations
belong to host lifecycle and OS setup, not model execution.

| Owner | Responsibility |
| --- | --- |
| Application and Guard | Enabled preference, frozen control mode, service identity, selected tool version and permission check before every dispatch |
| Desktop Manager | Verified runtime and service, owned native session, serialized calls, cancellation, cleanup and no retries |
| Cua Driver | Native argument semantics, application/window targeting, snapshot and token validity, capture coordinates, input semantics and native refusal/result details |
| Generic MCP/tool/media boundaries | Published schemas, JSON argument handling, bounded multimodal results, image transforms, source retention and paged result readback |
| Model and caller | Choose an operation from current observations and explicitly verify the task postcondition before further input |

There is no second AICE app/window allowlist, observation consumption rule,
element-token registry, capture mapping or refusal-continuation state machine
on the model path. A new user message does not require rediscovery merely to
satisfy a local allowlist. Native sessions and references still expire; obtain
fresh state when continuing a task, and use Cua's response to resolve stale
references. Reading historical source does not renew a native reference.

Upstream options such as `max_elements`, `max_depth`, `snapshot_id`,
`capture_id`, alternative targets, launch arguments and file output remain
available when advertised by the pinned platform schema. These calls use the
same service/tool Guard policy as other managed operations. GUI and native
file effects are not constrained by workspace file policies.

`background_only` rejects explicit foreground delivery and desktop-wide input;
`foreground_allowed` permits those requests without requiring a previous
allowlisted refusal. The setting does not make every platform or application
support the requested route. There is no automatic foreground fallback. A
text-only model must request `include_screenshot:false`; prefer semantic input
when no current image is available. These are frozen run capabilities, not decisions inferred from a native
error code.

The application supplies the Run's actual frozen control mode in the model
prompt before discovery, including whether foreground delivery is unavailable.
The builtin Computer Use guidance prefers an observed semantic action (such as
macOS `confirm` on a text field or `press` on a submit button) over process-wide
keys or coordinates. A `same_pid_keyboard_ambiguity` refusal concerns routing
keys among sibling windows; it is not evidence of focus loss. An unresolved AX
window can still have a valid screenshot without a usable background input route.
Models should report the specific blocked step when supported routes are exhausted.

Selected tools persist while present in the current request's definitions.
Discovery is needed for missing definitions or explicit lifecycle/catalog
invalidation, not before every action. Known semantic targets should use a
focused `query` and `include_screenshot:false`; screenshots remain available for
visual grounding. Query filters output rather than guaranteeing a cheaper tree
walk. Timed-out trees need an appropriate walk budget; clipped model results
can be read through `tool_result_read`. These are caller instructions, not
automatic argument rewrites, retries or guarantees of model task completion.

Coordinates follow the upstream operation's coordinate frame. For screenshot
pixels, use the original Cua screenshot dimensions and capture identity where
the schema supports it. The generic media layer describes original and displayed
sizes if it resizes an image; AICE does not reverse that transform inside CUA
calls. In 0.30.4, macOS window-scoped foreground pixel clicks use exact-window
HID activation, move the physical pointer and leave it at the target while
attempting to restore the previous front application. Background pixel input
does not move that pointer; Tk targets refuse it. This behavior does not change
AICE's background default or authorize an automatic foreground retry. Structured results, including degraded-state and escalation guidance,
remain source data. The generic model budget may clip their initial view; use
`tool_result_read` to retrieve the necessary section with the supplied selector.

Calls require explicit connection/session admission through discovery and do
not reconnect while executing. The final Guard check runs after acquiring the
native-call gate and again immediately before transport dispatch. Transport
failures or catalog changes retire the connection. Known native session-expiry diagnostics also retire it
while preserving the original result; the next explicit discovery establishes
a fresh lifecycle. Other native domain errors and degraded observations are
returned unchanged, without being recast as local target-admission failures.
A successful RPC is not proof of the application's postcondition. Each
post-action observation is another ordinary tool call with its own Guard check.

Local setup retains bounded window discovery and image validation for its
explicitly selected capture probe. Those helpers do not define model execution
semantics. Native fixtures call production `Tools` / `CallChecked`; polling,
post-action reads and independent assertions belong to the fixtures.
Deterministic tests establish these boundaries, not native desktop or actual
model task acceptance.

The application has an explicit managed-catalog constructor for a native `Run`.
It supplies `managed:cua` with source `managed:computer-use` to the same catalog,
search, generic result mapper and Guard. The connection fingerprint includes the
pinned Driver/protocol and application-owned Manager/settings identity. Permission scope
also includes the Run's actual immutable control mode, the reviewed operation
inventory, configuration restrictions and managed policy revision. These identities
are application-owned; no native session label or configured remote annotation
can grant authority. The constructor borrows the Run and performs no native I/O.

The application's context-bound constructor accepts only its own live native
Run. The identity stays stable across Runs with unchanged settings. A successful
Computer Use enablement/mode publication rotates that identity and removes its
Guard policy under the idle settings reservation. A global MCP restriction change
also invalidates the managed binding, including when rebuilding the runtime fails
after a saved change. Changes or reconnections of unrelated ordinary MCP services,
failed saves and unrelated scalar settings preserve the existing identity.
An old context cannot reconstruct a catalog after invalidation,
and restoring the previous settings cannot revive an old catalog or permit.
A disabled child Run shadows any inherited desktop capability. The native
managed harness uses this application-owned constructor; no caller-selected identity
is needed there. Print and interactive main runs use this same binding.

Only that explicitly injected backend can inherit Computer Use authorization for
the 11 reviewed names. Additional discovered names are denied, ordinary services
named CUA still require ordinary authorization, and ordinary configuration cannot
supply the reserved identity. Whole-service/tool restrictions with source `*`
apply to the managed service too. Discovery cannot undo revocation, owner/mode
replacement, or an explicit same-binding tool denial. A changed native catalog
invalidates already selected versions before dispatch. Synthetic application
checks cover these transitions and the real Loop/Guard/generic-result/Session
path, including exact structured values after reopening history.

Production composition exposes the MCP discovery tools when Computer Use is
on, even with no ordinary configured MCP service. Run binding adds a short local
service summary and borrows the native Run without connecting or listing tools.
Only explicit discovery admits the service and selects operation schemas for a
later model round. Closing the catalog precedes native Run cleanup. The typed
`desktop_*` tools are absent from production requests; enabling and disabling
Computer Use updates the next Run through the existing settings publication.
MCP status displays this preference even when disabled, without native I/O or
readiness claims. Its Computer Use menu item and `/mcp desktop` focus the same
setting as `/desktop`; ordinary MCP mutations cannot edit the managed entry.
Ordinary configured MCP connections cannot target that native endpoint, even
while Computer Use is disabled. Add/replace and connection startup share this
application check. Explicit independent endpoints/direct runtimes retain ordinary
authorization; arbitrary host programs are outside this endpoint reservation.
See [connection admission](mcp.md#implementation-status) for recognized identities
and limits. Full platform/model acceptance remains pending.

### Version-matched guidance

The embedded `computer-use` Skill is AICE-authored guidance for the managed
Cua Driver 0.30.4 interface. Its description explicitly applies only
when `managed:cua` is available. The normal startup catalog lists its name and
description; the `skill` tool loads its body on demand. No remote resource,
initialization instruction or desktop setting automatically activates it.
User/project copies follow the existing trusted-source precedence and may shadow
the guide; this cannot replace the execution constraints in desktop or Guard.
A test compares the guide's version with the desktop and installer pins.

The guide covers next-round tool selection, upstream references and coordinate
frames, generic result readback, explicit post-action verification, configured
control mode and unknown outcomes. It requests no extra approvals or exemptions
beyond the user's task and existing Guard policy. Loading guidance creates no
connections or grants and adds no alternate automation route.

Native and real-model procedures are in
[Verification](collaboration.md#computer-use-checks). Earlier native and
scripted/real-model results predate the current thin forwarding boundary;
they establish only their recorded implementation and fixture scope. The
current macOS scripted three-form gate passes as recorded below. Real-app tasks,
degraded observations and explicit foreground recovery still need acceptance.
Default checks do not open desktop windows or call providers.

## Run, reference and result contracts

A run binding freezes its control mode and model image support without native
I/O. The manager serializes each managed operation and its state updates. It lazily
starts a uniquely named Driver session, ends only that session on run close,
and keeps its connection available until disconnect or manager close. A cached
status read never enumerates, captures, launches or requests authorization.
Public manager construction requires an application runtime resolver. It only
reuses a verified installation and admits a compatible service; a pinned
proxy alone cannot prove a shared daemon's version or permission mode.

Managed tools use the pinned upstream operation schemas with a host-owned
session. AICE checks JSON object shape, published field names and its frozen
capabilities; Cua validates native argument and operation semantics. There is no typed model adapter or alternate executable
desktop route.

Native sessions can expire independently of the reusable MCP connection. The
pinned runtime defaults to five minutes of inactivity with a thirty-second
maintenance sweep. Discovery operations without a public session field use
the transport's implicit session. AICE sends no keepalives and does not replay
failed discovery or input. After a transport/catalog failure, explicit discovery
can establish a new connection and native session label. A known native
session-expiry result retires the connection without replay;
only a later explicit discovery establishes a replacement. Other native domain
errors are retained as returned without resetting the session.

A new main run owns a new session. Previous transcript references are historical,
so callers should refresh native state before continuing work. Cancellation or
a lost response can leave an action's effect unknown. Observe the application
before deciding what to do next; never infer that an unreturned action did not
execute. Cleanup closes only AICE's connection and owned session, not the shared
daemon or user applications.

On macOS, the first desktop use also obtains an exclusive AICE occupancy lock
at `~/Library/Caches/cua-driver/.aice-desktop.lock`, before runtime resolution or
connection. The lock waits at most two seconds and responds to Stop; contention
returns `desktop_busy` without native discovery or input. It spans the run's
model decisions and connection recovery, so another AICE instance cannot
concurrently issue managed input during that run. Multiple bindings in one manager share ownership until
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
opaque target reference can trigger one screenshot. Local media validation and
the selected-window capture binding must succeed. No click, typing,
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
access but cannot request task screenshots.

The application constructs one manager without native I/O and binds it only
when a main run actually starts. The context carries both the owner identity
and frozen run backend; closing a run cancels that context before bounded
session cleanup. Print and interactive shutdown also close the manager.
Settings reads and input preparation create no native run. Tool arguments cannot
change control mode, install helpers or request authorization. Side questions
retain their existing tool-free model boundary.

Startup, Web replacement and desktop preference changes share one tool
composition function. Settings prepares the candidate Loop/tools/prompt before
saving and publishes under its existing shared-resource reservation. A failed
save leaves the previous snapshot active. Managed discovery requires a live
owner binding, and each operation passes the generic identity-bound Guard.
Missing/closed bindings, disabled capability and explicit deny cannot be lifted
by `--yolo`. This does not enforce workspace file policies
inside native applications; [Guard behavior](execution-sessions.md#tool-execution-boundary)
describes that boundary.

### Native helper contracts

Production `Run` exposes managed MCP discovery and calls. Local Setup retains
bounded window discovery and selected-window observation for its capture check;
these read-only helpers are not model tools. The model path does not reuse
setup's local reference maps or coordinate conversion.

Native acceptance fixtures use `Tools` and `CallChecked`, with explicit reads
and postcondition assertions. Fixture convenience helpers may sequence discovery,
input and observation or poll a condition; those helpers compile only in tests.
Their timing and convenience results are not production tool or Session formats.
There is no production `Act` or `Apps` API.

A post-action read is independent of the action result. Its failure cannot
replace a returned or unknown execution state. Neither the managed boundary nor
the generic client retries a mutation. Foreground mode permits explicit native
foreground requests; it does not certify that a preceding uncertain action had
no effect. Driver escalation advice must be read together with execution state
and current application state before choosing further input.

### Production presentation

Text `--print` progress reports managed desktop tool names, status and elapsed time;
it omits argument details so input text and observation queries are not copied
to stderr. Full calls and results remain in the Session. Explicit
`--output-format json` retains the existing [NDJSON event contract](contracts.md#print-ndjson-events),
including arguments and bounded result text; it is transcript output and can
contain window contents, not a content-free diagnostic stream.

Test-only native acceptance helpers aggregate local timing across explicit
managed operations:
executor queue admission, mutation RPC, condition/window polling, final observation,
and total call time. Polling includes read-only RPCs and timer intervals; final
observation includes capture, decoding and image processing. The mutation RPC
measurement includes transport overhead and connection retirement on failure;
it is not a measurement inside Cua's native input implementation. Total also
includes local validation and release cleanup. Durations remain available after
failure or cancellation, and no action is retried to obtain a measurement.
These fixture fields are not production tool or Session data, with no background
sampler or additional telemetry. Native Manager acceptance tests print only
operation names, synthetic target indexes and these durations. Model output
wait, Guard time and next-request preparation remain outside this boundary.
The [opt-in model gate](collaboration.md#explicit-real-model-desktop-gate) now
measures request, Guard, tool and `managed_call_ms` intervals with explicit
provider/Loop/Session boundaries. Its scripted gate checks all 18 managed native
call timings and request measurement coverage. Historical typed reports retain
Manager phase measurements at their recorded source commit. Historical
three-form real-model results are summarized in the platform evidence below;
they do not validate the current forwarding boundary. Isolated network timing and controlled cold/warm comparisons
remain unverified.

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

Foreground and desktop-wide input still need native acceptance for each
platform and route. Schema availability and passing offline tests are not claims
of native readiness.

## Settings and task continuation

Shared boundaries: `Config.WithPatch` / `SaveSettingsPatch`,
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

The 0.30.4 upgrade passed full Go tests and vet, the macOS metadata inventory
and absent-service checks, and read-only native same-Run reconnect on
2026-09-30. The installed signed App retained the existing Accessibility and
Screen Recording grants. Linux arm64 private installation and reuse passed in
a disposable container. These checks do not establish Calendar task completion.

A subsequent 2026-09-30 visual diagnostic on 0.30.4 exercised four synthetic
AppKit clicks through the managed MCP boundary: element-token and screenshot
coordinates, each on the primary Retina display and the left secondary display.
All four independently recorded exactly one commit and preserved the foreground
sentinel with zero focus losses. Coordinate requests hit-tested the button and
also used AX; this does not establish raw pointer-event delivery. Independent
ScreenCaptureKit captures of only the fixture and Driver overlay showed the
primary-display cursor at the button location without an obvious offset, but
no cursor on either secondary-display action. The overlay remained at desktop
points `(0, 0, 1512, 982)` while the secondary button was at `(-1740, 691)`.
This reproduces missing multi-display feedback, not the earlier reported visible
offset or general Calendar behavior. The temporary diagnostic bypassed the
existing lifecycle test's session-label assertion, which could not find its
session in the operator listing; it does not establish renderer lifecycle
acceptance. That assertion needs investigation before the stock gate can pass
on 0.30.4.

The current runtime pins Driver **0.30.4**. The native results below were
recorded on **0.29.1** unless a newer version is explicitly named; they do not
constitute native acceptance of the upgraded Driver. The current thin MCP boundary
has passed the macOS scripted and discovery-expiry gates below; other native
evidence predates this boundary and establishes only the recorded adapters and fixtures. Reproducible
test commands, fixture boundaries and model budgets are owned by
[Verification](collaboration.md#computer-use-checks).
Historical timing samples and superseded local reference rules remain in Git
history; they are not current product guarantees.

| Platform | Established evidence | Remaining acceptance |
| --- | --- | --- |
| macOS universal | Signed artifact, Gatekeeper and 15-tool inventory; operator-granted Accessibility/Screen Recording; AppKit/WebKit semantic inputs, single pixel click, resize refusal/recovery and background scroll; scripted CLI/Guard/Session path, cancellation and shared-service survival; Settings repair with existing grants; current thin adapter passes the scripted AppKit/WebKit/AppKit gate below | Real-model and real-app task completion on the current adapter, degraded-observation/explicit-foreground recovery; double/right-click defects and drag repeatability below; first installation and physical system-dialog interaction; interrupted gestures, multi-display/Space behavior, overlay compositing/animation, broader app/IME/input coexistence |
| Linux arm64 | Private installation and headless inspection; isolated Debian 13 Xvfb/Openbox/AT-SPI/GTK owned-stdio and shared-service paths; semantic value writes, ASCII insertion, pixel click/resize refusal; scripted CLI/Session replay; selected-window Settings setup | Current thin adapter; Unicode insertion and launch defects below; keyboard/gesture routes without independent input devices; real models, physical desktop/IME, other toolkits, Wayland/compositors |
| Linux amd64 | Archive/executable hashes, ELF dependency inspection, synthetic installer tests and cross-compilation | Native installation, runtime/service admission, input/capture and compositor acceptance |
| Windows amd64/arm64 | Archive/executable hashes, Authenticode installer checks in code, source-reviewed status schemas, synthetic peer/status/UI tests and cross-compilation | Native installation/signature trust and named-pipe/UIAccess/session checks; setup/action integration and native input/lifecycle acceptance; actions remain unavailable |

On 2026-09-29, the current `TestNativeMacManagedModelHarness` passed on macOS
with race detection and Driver 0.29.1:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacManagedModelHarness$' -count=1 -v
```

The scripted run made 25 model requests and 18 native operations, delivering
nine images. Independent AppKit/WebKit/AppKit state confirmed all three exact
values and exactly one commit per target. The foreground sentinel recorded
zero losses; the shared service survived and raw results/images replayed from
Session history. No real model or paid provider was called.

The fixture explicitly requested `max_elements:200`, `max_depth:15` and
`max_image_dimension:1600`, and used queries for `Task value` and `Commit`.
These are fixture choices, not production defaults or AICE-imposed observation
limits. Two preceding attempts without narrowed queries reached 53 requests
and about 111.7k context tokens, exceeding the 111,616-token limit. The first
also recorded one foreground loss whose cause was not attributed. Those
attempts failed; the later independent complete pass does not erase them or
establish efficiency for unfiltered observations. This gate does not exercise
the user's Calendar task, a real model, degraded-window foreground recovery or
arbitrary applications. Those acceptance gaps remain open.

The current boundary also passed `TestNativeMacDiscoveryIdleRecovery` with race
detection on 2026-09-29 in 366.63 s. After six idle minutes, the daemon returned
the pinned `tool_invocation_failed` session-ended diagnostic for discovery.
AICE retired the connection and the next explicit discovery established a fresh
connection/session in the same Run. Shared-daemon identity remained unchanged;
no screenshot, input, launch or model request occurred. This verifies implicit
discovery expiry, not an interrupted action or foreground recovery. The procedure
is in [Verification](collaboration.md#native-discovery-idle-recovery).

Earlier managed scripted gates verified three independent form values and
exactly one commit each, nine images, foreground sentinel preservation,
shared-service survival and Session replay. Their bounded views required paged
readback. A matched real-model three-form comparison also passed within its
recorded model/budget scope, while earlier attempts failed budget, scope or focus
checks. Neither establishes general model efficiency or arbitrary application
reliability. The [manual record](desktop-manual-checks.md#当前验收结果) describes
one operator-reported TextEdit → Safari → VS Code task and its later discovery
failure; it does not replace automated focus or pixel-action assertions.

macOS observations contain actionable nodes and may omit passive labels even
when a response is otherwise usable. AppKit tests therefore verified native
widget state independently. On WebKit, `set_value` echoed an AXValue without the
expected DOM effect; insertion with `type_text` on an empty field completed the
fixture task while still reporting `effect:unverifiable`. Verify the business
state rather than treating an AX echo or successful RPC as completion.

The native cursor gate established session renderer visibility and cleanup.
A separate capture showed the blue cursor on the transparent host surface, but
the combined manual run failed its foreground sentinel. These establish neither
desktop compositing/animation nor physical-pointer independence. Two later focus
diagnostic runs overlapped operator input and cannot attribute focus changes to
Cua. Tests retain independent state assertions even when a focus check fails.

The pinned macOS renderer has a concrete multi-display limitation: its overlay
window and pixel buffer cover only `NSScreen.mainScreen` at startup, and painting
uses no desktop-origin offset. Its visibility predicate also excludes sufficiently
negative coordinates. See the [0.30.4 renderer source](https://github.com/trycua/cua/blob/bf6c76786d938070f4ecf1e44004752f69f518b8/libs/cua-driver/rust/crates/platform-macos/src/cursor/overlay.rs).
An application on a display to the left of the primary screen can therefore
receive background input without a visible agent cursor. AICE neither moves
user windows nor changes input coordinates to compensate. Fixing this requires
a reviewed Driver change and native visual verification on secondary displays;
passing the existing cursor lifecycle gate does not cover it.

The cancellation gates observed a committed click while its response remained
pending, preserved an unknown result and verified no replay after cancellation.
Settings Stop also cancelled explicit read polling through the actual CLI/TUI.
Piped terminal input does not establish physical Stop keys, IME or interrupted
native gesture cleanup. Natural native-session expiry and owned-proxy crash were
also exercised under the earlier adapter; its local reference retirement is no
longer the model execution contract. The current forwarding behavior needs its
own native recovery check.

### macOS input limitations

On the pinned Driver, background pixel double-click delivered two left event
pairs but caused a temporary foreground-sentinel activation loss. Background
right-click delivered two right event pairs for one requested click. Both made
exactly one native RPC and returned `effect:unverifiable`. Restored focus and a
non-error response do not satisfy continuous-focus or exact-count acceptance.
The pinned source permits activation/restoration in the background raw-left
route and uses both SkyLight and public posting for right events; those are
plausible mechanisms, not a native transport trace establishing causation.

Background AppKit scrolling passed an independent offset assertion. Background
drag returned `background_unavailable` before input with no slider movement.
One explicitly enabled foreground drag moved the slider and restored focus;
a later probe failed movement/restoration with another app foreground. Its
cause was not attributed, so repeatable foreground-drag acceptance remains
open. The current control mode allows explicit foreground requests, including
when the Driver recommends that route after degraded observation; no automatic
fallback or replay is performed.

Single screenshot-coordinate clicks, resize rejection and one negative-origin
window translation passed earlier fixtures. They do not establish multi-display
scale transitions, cropped images, remaining pixel operations or source/display
coordinate handling in the current generic path.

### Windows action admission gaps

Windows actions remain unavailable. The pinned
[`LaunchAppTool`](https://github.com/trycua/cua/blob/7a8f66ad04e62fccb18cca9965f2964fcaee124e/libs/cua-driver/rust/crates/platform-windows/src/tools/impl_.rs#L2034)
can use packaged AUMIDs or launch commands. Plain launch replies have a null
bundle ID and nested windows without PID; packaged apps can return an
ApplicationFrameHost PID. When no initial window exists, the fallback can choose
a descendant or an unrelated name-related process. A pure-function diagnostic
of the fixed [`related_processes` helper](https://github.com/trycua/cua/blob/7a8f66ad04e62fccb18cca9965f2964fcaee124e/libs/cua-driver/rust/crates/platform-windows/src/win32/apps.rs#L84)
selected unrelated `gimp-3.3.exe` before the actual child `gimp-3.2.exe`.
This is source/algorithm evidence, not a native misdirected action.

Native acceptance must establish ownership for same-name processes, launcher
handoff and packaged host windows. Ordinary launch restores focus only after
activation; `active:false` is not a measurement. `start_minimized` uses another
process-family/name heuristic and is not accepted background coexistence.
Windows UIA timeout/refusal paths can report `effect:unverifiable` alongside
foreground advice; that advice cannot prove no earlier effect or justify blind
replay.

The existing read-only status path verifies named-pipe server PID, executable,
user SID, Windows login session and process creation time before/after the
MCP exchange. It neither starts nor elevates the service. UIA/PostMessage flags
are upstream constants, not target capability evidence; a nonzero login session
is not proof of an unlocked input-ready desktop. The native pipe/status tests
have only been compiled locally, so native status acceptance remains open.

### Linux input acceptance failures

The pinned Linux GTK input gate fails Unicode insertion. A 17-byte,
11-character `type_text` request returned success but produced only 10 bytes
and eight characters. The reviewed AT-SPI helper passes character count as the
insertion length, while the [AT-SPI contract](https://gnome.pages.gitlab.gnome.org/at-spi2-core/libatspi/method.EditableText.insert_text.html)
requires UTF-8 byte count. An isolated control experiment reproduced the prefix
with length 11 and exact text with length 17. This establishes the length-unit
defect, not a patched-Driver result. Passing `set_value` exercises another route
and does not establish correct insertion or its insertion fallback.

GTK background key/hotkey, pixel scroll and drag were unavailable in the isolated
Xvfb environment without `/dev/uinput`. Scroll offset and slider value remained
unchanged, while the foreground sentinel retained input. Structured refusal is
not successful input; button clicks may use semantic routing and do not prove
general pointer delivery. Do not attach host input devices just to make the
fixture pass. Supported independent input routes need separate acceptance.

The Linux success response can omit `screenshot_frame_valid`; explicit false
indicates a failed capture. The upstream pixel-click/resize fixture passed with
source capture IDs and matching PNG dimensions. Generic result forwarding keeps
these facts intact; it does not introduce a second platform-specific pixel
admission decision.

### Linux launch acceptance failure

The pinned Driver launched a temporary XDG fixture once, returned its window,
and permitted the requested value change and single commit. The app survived
Manager close. However, the foreground sentinel lost activation and remained
inactive after launch despite `active:false`. The field is a constant, not a
focus measurement; the helper does not prevent a mapped window from activating.
The test retained all injected keys, so it proves focus interference rather than
observed keyboard misdelivery. Preserve this failure separately from successful
input into already-open background windows. Other window managers and D-Bus
handoffs require their own evidence.

### Subsequent source review

The recorded 2026-09-27 review of
[0.30.1](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.30.1),
commit `039783f9221a08c0daf9cda65a460fc4f346fa6e`, found unchanged macOS
mouse/drag implementations and the same Linux insertion length calculation.
Its click route added explicit foreground HID delivery and a Tk background
refusal; these do not establish repairs for the observed AppKit defects.
The reviewed 0.30.2 nightly source also retained those paths. Neither release
was installed or accepted here; this is dated evidence, not a statement about
current upstream releases.

The same review identified [PR #4024](https://github.com/trycua/cua/pull/4024)
as a possible left-click focus repair, excluding right/middle/drag redesign.
[PR #2907](https://github.com/trycua/cua/pull/2907),
[PR #2646](https://github.com/trycua/cua/pull/2646) and
[issue #2206](https://github.com/trycua/cua/issues/2206) provide relevant upstream
history. None is an installed fix or substitutes for AICE's native assertions.
No artifact pin is changed by the thin adapter work.

### Shared runtime and application verification

Default tests use fake MCP peers and synthetic bytes. They check descriptor
forwarding, host session injection, mode and model-image restrictions, final
permission checks, cancellation, unknown outcomes, owned-child cleanup and
service-information projection. Local setup separately validates its selected
capture. These checks do not read or control user applications.

Application tests cover enabled/disabled publication, stale owner denial,
settings/Web recomposition, partial results, Guard and Session replay. Settings
CLI/TUI tests use fake external operations; the older native repair test reused
an installed signed App and existing grants. It does not establish first
installation or physical permission-dialog interaction. Occupancy tests use
temporary files and an independent helper process to check OS lock ownership;
they do not isolate third-party clients or the user.

Native tests require an explicit opt-in and synthetic task scope. New real-model
acceptance must use an authorized provider/model and budget, retain artifacts,
and verify task effects independently of tool return status. See
[Verification](collaboration.md#explicit-real-model-desktop-gate).
