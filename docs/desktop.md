# Computer Use integration

Computer Use is being integrated with Cua Driver. The current implementation
contains the pinned distribution metadata, a private persistent stdio client,
User-only configuration fields, and a privately constructed desktop manager.
The Settings fields are visible but disabled
until setup and execution are connected; no model desktop tool is exposed yet.
The implementation plan's complete native acceptance remains open.

## Ownership and connection contract

`internal/deps` owns immutable artifact selection; see
[provenance and licensing](../internal/deps/cua/VENDOR.md).
`internal/desktop` owns the Cua connection and its exact child handle. The
application will own configuration publication and run bindings. The existing
Agent Loop, Guard, media pipeline and Session remain the execution boundaries.

The client uses official Go MCP SDK v1.6.1, sends legacy `initialize` with
`2025-06-18`, checks the returned protocol and Cua identity/version, then discovers
tools once with bounded pagination. It does not mix modern `server/discover`
or per-request protocol metadata into that session. Responses retain text,
image bytes, structured content and domain error status. No tool call retries
at this transport boundary, including after cancellation, timeout or EOF.

Stdout is a private NDJSON pipe capped at 24 MiB per message before JSON/base64
decoding. SDK diagnostics and child stderr are discarded rather than duplicating
window contents or typed text in logs. Process environment construction excludes
model credentials, loader injection and inherited Cua permission overrides.
Owned children disable Cua telemetry and update checks.
Closing the connection waits for or terminates only its owned MCP child; it does
not stop a shared service. A service endpoint is required explicitly.
The proxy uses the pinned release's `--embedded` switch solely to refuse
automatic service launch if that endpoint disappears. It never uses `--direct`
or claims a host bundle identity; the standalone daemon retains its own TCC
identity and permission mode.

A private macOS connection preflight now checks the existing service before
admission. The bounded public `status --socket` command supplies content-free
mode, policy and PID facts; its pinned text format is parsed only for management,
never for desktop action results. The persistent MCP connection then checks
`get_config` for the actual daemon version/platform and `check_permissions`
with `prompt:false` for App executable, bundle identity, PID and OS grants.
A second status read rejects a changed service. External policies/manifests,
non-standard mode, identity mismatch and missing grants remain distinct errors.
No probe enumerates windows or captures; granted TCC booleans are not evidence
of successful capture. This preflight is not yet connected to setup or the app.
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
The manager constructor remains private pending setup/application wiring and
native service acceptance; a pinned proxy alone cannot prove a shared daemon's
version or permission mode.

Window discovery issues opaque references for returned native pid/window pairs.
Application discovery also returns bounded installed/running app identities,
including localized names; bundle-ID matches include their running windows.
Launch accepts only a locally issued app reference, resolves its known bundle
ID, and consumes that reference before one native request. A ready single window
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

The current typed actions cover background click/double/right-click, semantic
text insertion and value setting, exact-window keys/hotkeys and semantic scroll.
Key names and modifier combinations are validated before dispatch; unrelated
action fields are rejected. Each mutation consumes its observation before
dispatch and returns its Driver facts plus a fresh observation under the same
execution reservation. Post-observation failure preserves the action response.
A lost response returns `outcome=unknown`; it never retries the mutation.
`outcome=returned` means an RPC response arrived, not that a business effect was
independently confirmed. Driver `isError`, structured details and bounded text
remain separate from transport and follow-up-observation failure.

Semantic condition waits hold the same executor and repeatedly observe the
exact window within a caller-selected deadline of at most ten seconds. They
report `satisfied`, `unsatisfied` or `unknown`; a missing match in an incomplete
projection stays unknown. Polls request no images. A requested final screenshot
is captured once only while time remains, and its semantic condition is checked
again. An expired deadline can return the last valid semantic observation;
a failed refresh never returns older execution references. No hard sleep or
image-stability heuristic stands in for a condition.

Observations project at most 200 semantic elements and 96 KiB of their text,
with visible truncation/incompleteness. One screenshot goes through the existing
media validator and image content pipeline. Coordinate mapping reverses only
AICE's image resize. It requires matching actual image dimensions and a native
capture ID; it never adds screen offsets or reapplies Retina scaling. A failed
image can leave valid semantic references available. A text-only model cannot
request a screenshot or obtain a usable pixel binding.

Pixel scrolling, dragging, foreground assistance,
full schema capability validation and the public tool adapters
remain to be implemented. This partial manager is not yet wired to the Agent
Loop or declared native-ready.

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
so a future setup action need not acquire a second reservation.
Remaining changes belong in app tool/prompt/Guard composition,
TUI deep-link/setup/Stop handling, app run binding and the remaining typed tools.
Setup must use one configuration coordination reservation; Stop must use the
existing cancellation path. A Web rebuild must retain the same Desktop binding
as the tools and prompt it publishes. These execution changes have not yet landed.

## Platform evidence

| Scope | Evidence | Remaining acceptance |
| --- | --- | --- |
| macOS 0.29.1 universal artifact | Download hash matches fixed release manifest; AICE's temporary extraction/exclusive publication preserves strict signature, Cua signing identity and Gatekeeper acceptance; `--version` and advertised CLI/schema inspected | Settings installer wiring, signed installed service, system authorization, persistent MCP handshake against that service, synthetic multi-app task, background focus/input sentinel, native overlay, cancellation and cold/warm measurements |
| Windows amd64/arm64 | Published release digests pinned; upstream interactive-session requirements reviewed | Downloads, signatures where available, install/autostart opt-out, native UI/input/lifecycle tests |
| Linux amd64/arm64 | Published release digests pinned; upstream X11/Wayland capability distinction reviewed | Downloads, dynamic dependencies, AT-SPI/display detection, native compositor-specific input/capture/overlay tests |

The fixed source's platform matrix documents limitations for raw Wayland
background input and toolkit-specific paths. Structured refusal is not proof
that a promised action is supported. AICE must preserve `background_only`,
report unsupported routes and obtain a product decision if an upstream limit
prevents the agreed acceptance. Windows/Linux remain in scope; no native
validation is claimed for them.

The C0 local schema probe used an isolated HOME and disabled telemetry. Its
sandboxed invocation failed during AppKit pasteboard initialization; the
read-only schema export succeeded with normal GUI access. Neither invocation
read a window or executed a desktop action. No OS permission was requested,
App installed, user application controlled, or paid model called.

Default tests use raw fake MCP peers and synthetic bytes. Native tests must
be explicitly opted into and use synthetic applications and data. Cross-builds
and upstream test claims do not replace local native evidence.

Manager tests use synthetic windows and PNGs to verify single dispatch,
post-observation failure, cancellation, per-run cleanup, reconnection,
cross-run snapshot invalidation, malformed-image semantic fallback and exact
2100-to-2000-pixel coordinate conversion. These are offline lifecycle checks,
not native background-input or overlay acceptance.

The explicit macOS installer API now stages and verifies the signed App, reuses
a compatible existing installation, preserves conflicting files and respects
the current helper-download policy. It is not yet called by Settings or startup.
See [installation](installation.md#computer-use-helper-integration-in-progress).
Its opt-in artifact test passed on macOS using the pinned local archive. This
checks extraction, native signature/Gatekeeper verification and exclusive
publication in temporary directories; it never installs into `/Applications`,
starts a service, requests OS permissions or reads the desktop.
