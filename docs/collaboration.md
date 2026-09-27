# Verification and Collaboration

## Verification Commands

- Documentation-only changes: run `git diff --check`, check changed relative
  links and heading anchors, and verify behavior claims against their code
  owners. Run focused tests when a disputed claim needs reproduction; a docs
  edit alone does not require the full Go suite.
- After Go code changes: format only modified Go files, then run `go test ./...` and `go vet ./...`.
- When `.golangci.yml` exists, also run `golangci-lint run ./...` and fix new findings rather than suppressing them.
- For concurrency, cancellation, channels, or shared-state changes, run `go test -race ./...`.
- If a test file changes, run the focused test while iterating, then the full unit suite before handoff.
- Tool and user-facing verification that exercises `grep` requires `rg` on `PATH`.
- Mark real-provider tests with an integration build tag and run them explicitly. Default tests must use faux providers and must not read provider credentials.
- Verify CLI/TUI work through the actual user-facing command. Do not treat package tests alone as proof that interactive behavior works.
- Do not invent build, release, changelog, or publishing commands before the repository defines them.

The [CI workflow](../.github/workflows/ci.yml) and
[release workflow](../.github/workflows/release.yml) call the same
[verification workflow](../.github/workflows/verify.yml), which owns the Linux,
macOS, and Windows matrix, Go setup from `go.mod`, ripgrep installation, race
tests, vet, and offline installer checks. It uses `workflow_call` without inputs
or passed secrets and requires only `contents: read`. Both callers use a local
workflow reference so verification comes from the same commit as the caller.
CI runs on pushes to `main` and on pull request creation, updates, and reopening.
Each run checks all three platforms. Other branch pushes do not trigger CI;
open a pull request to verify a development branch before merging.
Release builds run alongside verification; publishing requires both `test` and
`build` to succeed, and only the publishing job has `contents: write`.
The publishing job runs only for pushes of `v*` tags. Manual
`workflow_dispatch` runs verify, build, and upload bundles but skip publishing,
even when dispatched against a tag.
A local pass proves only the tested platform; report
unavailable tooling or platform checks rather than claiming they passed.
Windows runs `go test -race -p 1 -parallel 2 ./...` to limit concurrent test
processes and parallel cases after intermittent ripgrep `STATUS_NO_MEMORY`
(`0xc0000017`) exits on hosted runners. This retains every test and race
detection; goroutines within each test still run concurrently. Linux and macOS
use the default test parallelism.
The Guard-to-grep path acceptance test and its subtests run sequentially, outside
the parallel application-test batch. These cases launch real ripgrep processes;
the Windows race runner has also reported `STATUS_NO_MEMORY` with two parallel
cases. Keep the real execution assertions and all path cases in this test.
Path spelling tests normalize accepted host separators before comparison, while
still checking literal names such as `~`, `@`, and Unicode characters exactly.
Symlink tests compare link targets using host separators. Replacement tests
retain the original file through a hard link and verify its content is unchanged;
they must not infer replacement from pre-write `os.Stat` and post-write
`os.SameFile`, because Windows can load file identity lazily from the reused path.
The [release workflow](../.github/workflows/release.yml) owns release build and
packaging commands. Harbor has its own [integration guide](../integrations/harbor/README.md).

For macOS clipboard changes, run the AppKit bridge check with an isolated
pasteboard (it never reads or changes the user's general clipboard):

```sh
go test -tags=integration ./internal/tui -run '^TestMacClipboardNativeFormats$'
```

The ordinary clipboard tests use synthetic input and bounded helper processes.
Re-executed test helpers use a generous watchdog to accommodate race runtime
startup and exit delays on CI; this does not change the TUI clipboard deadline.
Cancellation is tested explicitly, and watchdog expiry must not count as an
expected helper failure or output-limit rejection.
Linux and Windows clipboard behavior still requires verification on a desktop
of that platform; cross-compilation alone does not verify native helpers.

The CLI-driven login test gives the complete multi-step flow a one-minute
watchdog, including per-key rendering under race instrumentation. Each menu and
prompt must still appear before the test sends the next input.

## Installer checks

Installer tests use local release fixtures and simulated network failures; they
do not download releases or change the user's PATH:

- macOS/Linux: `sh -n scripts/install.sh` and `python3 scripts/test_install.py`.
- Windows: `powershell -NoProfile -File scripts/test_install.ps1` and
  `pwsh -NoProfile -File scripts/test_install.ps1`.

The shared verification matrix runs these for both CI and release on their
native platforms, including Windows PowerShell 5.1 and PowerShell 7. They cover
release pinning, checksum rejection, copy/replacement failures, cleanup, and
path handling.

## Offline capability checks

The default suite's [binary print acceptance](../cmd/aice/main_process_test.go)
builds AICE once into a temporary directory and invokes `--print` against local
HTTP fixtures. It checks stdout/stderr separation, process exit codes, and
`--yolo` preserving the secret-file deny while ordinary reads still succeed.
It also checks token exhaustion before tool execution and repeated-tool stops
with the default threshold, an explicit threshold, and detection disabled.
Turn-limit checks cover settled tools, explicit zero, and natural completion
at the final permitted request.
Each invocation uses a temporary home and workspace, an environment allowlist,
disabled helper downloads and update checks, and explicit project distrust.
The build reuses the Go test toolchain, race mode and caches with module downloads
disabled. Its five-minute build watchdog is separate from each invocation's
20-second runtime watchdog; build timeout diagnostics identify that phase.

[Stream failure tests](../internal/agent/stream_failure_test.go) distinguish
unaccepted tool deltas from valid calls retained in a terminal assistant:
neither executes on failure, while only retained calls receive paired results.
[Welcome initialization](../internal/tui/welcome_test.go) executes its finite
startup commands with virtual time. [Terminal rendering
tests](../internal/tui/terminal_rendering_test.go) run Bubble Tea with captured
output, including permission and side-panel transitions. These tests exercise
the renderer but do not replace native terminal, desktop clipboard or IME checks.

Configuration-lock and replacement retry tests inject filesystem errors and use
a virtual clock to cover Windows access denial, sharing violations, cancellation,
and timeout on every platform. A native Windows test holds a settings reader open
to verify failed replacement preserves the original file and cleans up the lock
and temporary file. Lock tests do not require an open directory handle to
prevent recreation; that behavior varies across Windows filesystems and versions.
Real temporary directory tests still cover lock ownership and concurrent token refresh with
a local fake OAuth server, without accessing user credentials or remote APIs.
The concurrent settings-process test retries only Windows sharing violations
from its polling reader while writers are active, within the test deadline.
Every successful read must contain valid JSON; other read errors fail immediately,
and a final read after writers finish verifies that all field updates survived.
Every exit path cancels and waits for the writer processes before test teardown.

The default Go suite includes [long-task acceptance](../internal/app/long_task_test.go):
one interactive input with an in-run correction, and one stateless print input,
each complete 200 scripted main model requests and at least three real application
compactions. Tools perform varying local reads; summary generation uses a scripted model.
Checks cover request pairing, retained requirements, budget, source counts, usage,
and reopening after summary cancellation or checkpoint failures. To inspect its
counts independently:

```sh
go test ./internal/app -run '200ModelRounds|CompactionFailureBoundaries' -v
```

The [Go HTTP service](../evals/go-service/README.md) and
[Python data CLI](../evals/python-cli/README.md) are separate engineering task
families. Each covers building from zero, extension, a reproducible defect, and
refactoring followed by another requirement. Their reference implementations and
independent acceptance are outside AICE's runtime module. Run their documented
self-checks explicitly; passing the root Go suite does not run these fixtures.
The guides own the task specifications, self-check commands, and review criteria.

Keep three kinds of evidence distinct: scripted models validate execution,
reference fixtures validate tasks and fault detection, and actual model runs
measure generated-code quality. Neither of the first two proves the third.
For model comparisons, preserve initial/final source and refactor-only diffs,
record settings and interventions, and review readability, change locality and
the need for each abstraction. Agree on models and cost before paid evaluation.

## Web checks

Web tests use local `httptest` servers, injected HTTP transports/clients, fixed
clocks and synthetic keys; they never resolve real hostnames, contact Exa or
read `EXA_API_KEY`. The [httpfetch tests](../internal/web/httpfetch/fetch_test.go)
answer public-looking hostnames with an in-memory transport while still
running the URL-shape and IP-literal checks before each request, so blocked
literals, redirects, `https` downgrade refusal, body limits, standard proxy
selection (`HTTP_PROXY`/`NO_PROXY`) and single-attempt transport errors are
covered without network access. TLS hostname verification itself belongs to
the standard transport. Application tests bind fake search and fetch backends
through the same factory list as production.

One paid request against the real Exa API is available as an explicit opt-in.
It needs both the build tag and the environment switch; a key alone does not run
it:

```sh
AICE_EXA_INTEGRATION=1 EXA_API_KEY=... \
  go test -tags=integration ./internal/web/exa -run '^TestRealExaSearch$' -v
```

It sends one fixed, non-sensitive query with three results and logs whether a
cost was reported. Agree on the charge before running it and report the result
separately from the offline suite.

## History performance checks

The synthetic history benchmarks use temporary sessions and generated Markdown;
they do not read user conversations or credentials. Run them serially, separately
from tests and other benchmarks, and compare repeated samples on the same host:

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkSessionBrowser$' -benchmem -count=5
go test ./internal/app -run '^$' -bench '^BenchmarkSessionTextQuery$' -benchmem -count=5
go test ./internal/tui -run '^$' -bench '^BenchmarkHistory(Markdown|Code)FirstView$' -benchmem -count=5
go test ./internal/tui -run '^$' -bench '^BenchmarkHistoryNavigation$' -benchmem -count=5
```

Catalog search measures repeated queries after warm-up against 134 files totaling
about 27 MiB of prose. First-view rendering constructs a fresh TUI projection for
each operation with 32 KiB or 128 KiB of mixed Markdown, or a collapsed code
block with 1,000 or 10,000 log lines. Report time and allocation
per operation separately from retained memory, and distinguish these fixtures
from actual terminal interaction checks.
Text-query benchmarks compare full lowercase conversion with prepared matching
for early hits, late hits, missing text and Unicode, without filesystem costs.
Navigation benchmarks cover restoration and mouse-wheel frames for 500 turns,
a long continuous paragraph, and a 1,000-item list. `TestSessionBrowserTUI`
exercises search, preview, read-only opening, scrolling and resumption through
the actual CLI and Bubble Tea with generated history and isolated settings.
On returning from read-only history, the test waits for a completed search with
a selected result before pressing Enter: the initial title-only batch may be
empty even when a later body match exists. While waiting for terminal text, it
requests resize repaints so asynchronous results can be matched as complete
frames instead of relying on renderer cell diffs.

## Git and Collaboration

- Multiple sessions may share this worktree. Preserve unrelated staged, unstaged, and untracked changes.
- Modify and stage only explicit files owned by the current task. Never use `git add .`, `git add -A`, `git stash`, `git reset --hard`, `git checkout .`, or force push.
- Never commit unless the user asks. Before committing, inspect `git status` and the exact staged diff.
- When the user asks for incremental commits, make one independently verified commit after each completed small step.
- When asked to commit, follow the repository's existing short gitmoji/conventional subject style and keep each commit to one intent. Do not use Pi package scopes or release conventions.
- If a conflict touches a file not modified for the current task, stop and ask the user instead of resolving it speculatively.

## Computer Use checks

The opt-in Cua artifact check uses an already downloaded, fixed-digest macOS
archive. It extracts into temporary directories, verifies signing identity and
Gatekeeper acceptance, and checks exclusive publication. It does not install
the App, launch a service, request TCC, or capture any window:

```sh
AICE_CUA_TEST_ARCHIVE=/absolute/path/to/cua-driver-rs-0.29.1-darwin-universal.tar.gz \
  go test -tags=integration ./internal/deps -run '^TestNativeCuaArtifactExtraction$' -v
```

Default Cua installer tests use synthetic archives and in-memory HTTP transports.
The pinned upstream release manifest is checked out with LF endings through
[`.gitattributes`](../.gitattributes), preserving its byte-for-byte SHA-256 even with Windows
`core.autocrlf` enabled. The checksum assertion is not normalized or weakened.
Artifact validation is distinct from native Computer Use acceptance; see the
[platform evidence](desktop.md#platform-evidence).

The Windows/Linux archive check runs on any host with all four previously
downloaded, pinned full distribution archives. It verifies archive digests,
bounded selective extraction and native-file digests without executing them,
installing into a user directory or evaluating OS signature trust:

```sh
AICE_CUA_TEST_ARTIFACTS=/absolute/path/to/archives \
  go test -tags=integration ./internal/deps -run '^TestCuaNativeReleaseArchives$' -v
```

Default installer tests cover tar/zip rejection, read-only reuse, modification,
additional-library and symlink refusal, cancellation, single-download concurrent
installation and cleanup. Native Linux/Windows unit tests check exclusive
publication against both empty and populated destination directories, without
executing Cua. They must run on those hosts; compiling them elsewhere is not a
passing execution result. The shared downloader test uses an isolated temporary
directory to check rejected-file cleanup, including Windows's close-before-remove
requirement.

On a native Linux or Windows host, the private-install acceptance test uses a
previously downloaded archive for that host and architecture. The production
installer still checks its fixed digest, runs native version/signature checks,
publishes into a test-owned directory and verifies download-disabled reuse. Its
in-memory HTTP transport reads only that archive; it does not contact GitHub.
The test does not launch a daemon, capture, request OS grants, install into the
user's helper directory or change autostart:

```sh
AICE_CUA_TEST_NATIVE_ARCHIVE=/absolute/path/to/native-archive \
  go test -tags=integration ./internal/deps -run '^TestNativeCuaPrivateInstallation$' -v
```

Linux arm64 passed this test, native exclusive-publication tests, concurrent
installation and rejected-download cleanup in an isolated Debian 13 container
on 2026-09-26, running as an ordinary user. The initial slim image's standalone
version probe failed for missing `libX11.so.6`; installing libX11, libXi and
libxkbcommon **inside that disposable test container** allowed the native check
to pass. AICE's installer does not perform that package installation. No display
or desktop D-Bus connection was supplied. This verifies headless installation,
not X11/Wayland input, accessibility or capture. Windows and Linux amd64 native
execution remain unverified.

Windows's ordinary `TestWindowsPeerRequiresExactExecutableAndPID` uses only a
temporary named pipe belonging to its own test process. It checks kernel-reported
PID, executable identity, session, creation time and cancellation without Cua,
GUI access or elevation. It is compiled here, not natively executed. These
identity checks connect directly to the created pipe without a pending server
accept; teardown closes the handle without waiting for an asynchronous operation.
Synthetic cross-platform tests cover service admission and capability projection;
the actual `TestSettingsUsageTUI` CLI flow also renders synthetic Windows status.
Neither establishes native Windows readiness.

On Windows, the separate opt-in status test requires the already installed pinned
private distribution and an existing standard-mode service running from that
same binary as the current user in the same login session:

```powershell
$env:AICE_CUA_NATIVE_STATUS = '1'
go test -tags=integration ./internal/desktop -run '^TestNativeWindowsServiceInspection$' -v
Remove-Item Env:AICE_CUA_NATIVE_STATUS
```

It performs two read-only inspections, checking the advertised source-reviewed
status schemas and leaving the shared service running. It never installs, starts
a service, creates a native task session, captures or requests UAC/UIAccess.
This native gate has not run here. A successful run would verify only status
admission, not interactive desktop, secure-desktop, input, capture or overlay behavior.

In an isolated Linux container with no user display or desktop bus mounted,
the headless service test starts one test-owned foreground daemon, reads status
twice through the production inspector, and reaps only that daemon. It requires
the explicit opt-in and an already checksum-verified binary; it never installs
or uses a user's existing service:

```sh
AICE_CUA_HEADLESS_CONTAINER=1 AICE_CUA_TEST_BINARY=/absolute/path/to/cua-driver \
  go test -tags=integration ./internal/desktop -run '^TestNativeLinuxHeadlessInspection$' -v
```

Linux arm64 passed this check in the Debian 13 fixture. Native Unix peer tests
also verified PID/executable mismatch rejection. Default raw MCP tests reject
Linux status-schema drift and prevent that connection from dispatching actions.
`TestSettingsUsageTUI` also passed on that Linux host using the actual CLI and
Bubble Tea with a synthetic backend; it opens `/desktop` and checks the separate
X11/AT-SPI status fields. This checks presentation, not a native desktop action.
The normal owned-process shutdown test waits for the child readiness message
before closing; the race runtime's artificial exit sleep is disabled only in
that synthetic child, so it cannot masquerade as a hung Driver.
The blocked-pipe shutdown regression models a pipe close that waits for child
EOF on every host. It verifies the shutdown deadline can still terminate and
reap the owned child when pipe closure blocks, and that repeated close preserves
the result. The native-pipe test retains the same ten-second watchdog and joins
its close goroutine after emergency termination on failure.

The opt-in X11 capability probe uses the pinned Linux Driver in a disposable
Debian container. Its runner installs Xvfb, Openbox, GTK and AT-SPI **only in that
container**, then runs as an ordinary user with a private display and D-Bus. Do
not mount the user's display, bus, home or input devices, and do not run the
package-preparation script on the host. Python/GTK is a synthetic test application,
not an AICE runtime dependency. With the native archive already downloaded:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags=integration \
  -o /tmp/aice-desktop-linux.test ./internal/desktop
docker run --rm \
  --mount type=bind,src=/tmp/aice-desktop-linux.test,dst=/probe.test,readonly \
  --mount type=bind,src=/absolute/path/to/cua-driver-rs-0.29.1-linux-arm64.tar.gz,dst=/driver.tar.gz,readonly \
  --mount type=bind,src="$PWD/internal/desktop/testdata/run-linux-probe.sh",dst=/run-probe.sh,readonly \
  python:3.13-slim sh /run-probe.sh /probe.test /driver.tar.gz
```

The test binary and archive must match the container's native architecture;
emulation or cross-compilation is not native execution. The runner verifies the
archive's fixed SHA-256. `TestNativeLinuxBackgroundProbe` validates three GTK
processes through one persistent stdio connection: semantic Unicode edits,
button clicks, PNG dimensions, independent application readback, old-token
rejection and concurrent foreground core keyboard input. Read-only inspection
between observation and input must preserve the token. The separate
`TestNativeLinuxFocusSentinel` deliberately moves focus between its own windows
and verifies that the monitor retains focus-loss and misdirected-input evidence
even after focus restoration. Cleanup reaps test-owned children only.

The runner also executes `TestNativeLinuxManager` through the public production
constructor. It checks both an owned stdio runtime and reuse of a test-owned
verified shared service, three exact-window tasks, nine capture bindings,
consumed-reference rejection, one connection/session, owned-process cleanup and
preservation of the shared service. This test passed natively on Linux arm64 in
the isolated Debian fixture on 2026-09-26; its foreground sentinel retained every
concurrent core key with no focus loss in both modes. Static labels are not
part of Linux's actionable-element projection, so independent fixture state
confirms commits; a missing semantic match must not claim failure or completion.
Both native tests compare the full production Linux schema pin.
The native Manager gates on Linux and macOS also log per-action local timings
for queue admission, mutation RPC, condition polling, final observation and total
call time. These diagnostics contain no native request/response bodies and are
excluded from model/Session JSON. Virtual-time tests separately check phase
attribution for delayed input, lost replies, observation failures, queued
cancellation, condition deadlines and delayed launch windows. Native timings
are local harness measurements; neither these nor the aggregate discovery time
measure provider latency, Guard time or next-model-request preparation.
The optional `TestNativeLinuxInput` adds ASCII/Unicode insertion, single-key,
hotkey, screenshot-bound button click, resize/refusal/re-observation, pixel scroll
and drag cases. The latter two read actual GTK scroll offset and slider value;
fixture geometry supplies their points, not a visual model.
Pass `'^TestNativeLinuxInput$'` as the runner's third argument to run this gate.
Its full native run currently **fails** on Unicode insertion and unavailable GTK
keyboard, pixel scroll and drag delivery; see
[input acceptance failures](desktop.md#linux-input-acceptance-failures).
Do not change those cases into expected-success tests for refusal or truncation.
The passing button-click cases do not establish general pointer or keyboard
readiness. Fixture geometry supplies coordinates only to these tests; production
input still goes through Cua.
The optional `TestNativeLinuxLaunch` discovers a temporary XDG desktop entry,
launches it once, checks exact PID/window binding and a follow-up task, and
verifies the application survives Manager close. Pass `'^TestNativeLinuxLaunch$'`
as the runner's third argument. The full gate currently **fails** because launch
steals the foreground sentinel's focus despite Cua reporting `active:false`;
see [launch acceptance failure](desktop.md#linux-launch-acceptance-failure).
The wrapper, application files and launched process are test-owned; no system
desktop entry or host application is installed. AICE itself must leave launched
apps alive, and the test cleans up its synthetic fixture separately.
The Manager test additionally calls the public Linux setup API with a selector
limited to its synthetic target. It verifies a real capture, absence of invented
grant/service-launch facts, connection cleanup and shared-service preservation
in both modes. This selected-window setup passed in the same native fixture.

Default Linux runtime tests separately reject restricted/unknown/foreign shared
services without starting private fallback, and check owned process arguments,
fixed standard mode and credential filtering. They passed in a headless arm64
container as an ordinary user. `TestSettingsUsageTUI` also passed natively on
Linux with synthetic native operations: it follows disclosure, explicit window
selection, saved enable, Stop and explicit continuation through the actual CLI.
Unit tests cover cancellation before capture, foreign targets, missing images,
invalid mappings and partial external-step retention without Session creation.
The separate `TestNativeLinuxDesktopPrint` uses the production private installer
with the local pinned archive supplied by an in-memory HTTP transport, then the
actual print command, configuration loader, Guard, typed tools and native runtime.
Only the model is scripted; unrelated helper downloads are disabled. It selects
three synthetic GTK windows by returned PID/title, edits Unicode values and
commits once per window. Independent fixture state confirms results. Nine PNGs
must reach the next model request, and reopening the Session must recover exact
tool results/images, stable message parents and complete call/result pairs.
The foreground sentinel must retain concurrent core input with no focus loss,
and command completion must reap its private Driver. Text progress must not
duplicate desktop input contents. Run it with the same isolated runner:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags=integration \
  -o /tmp/aice-app-linux.test ./internal/app
docker run --rm \
  --mount type=bind,src=/tmp/aice-app-linux.test,dst=/probe.test,readonly \
  --mount type=bind,src=/absolute/path/to/cua-driver-rs-0.29.1-linux-arm64.tar.gz,dst=/driver.tar.gz,readonly \
  --mount type=bind,src="$PWD/internal/desktop/testdata/run-linux-probe.sh",dst=/run-probe.sh,readonly \
  --mount type=bind,src="$PWD/internal/desktop/testdata/linux-fixture.py",dst=/fixture.py,readonly \
  python:3.13-slim sh /run-probe.sh /probe.test /driver.tar.gz \
  '^TestNativeLinuxDesktopPrint$' /fixture.py
```

This is scripted-model native execution, not actual-model visual reasoning or a
fully native Settings installation workflow. Native launch/pixel/keyboard/drag
actions, physical input and other Linux compositors also need separate evidence.
See the [platform evidence](desktop.md#platform-evidence) before claiming support.

The separate `TestNativeLinuxDesktopSetupTUI` drives `/desktop` through the
actual CLI and Bubble Tea with the production desktop constructor, installer,
setup API and Settings writer. Archive delivery uses the local pinned file
through an in-memory HTTP transport; unrelated helper downloads are disabled
by the test harness. No native operation is replaced. It checks that disclosure
precedes installation, the default Cancel at window selection retains installation
without capture or enable, and retry reuses that installation before an explicit
window choice. Successful native capture must precede saved enable and appear
as historical verification in the status view. No model request or Session may
be created, and both cancelled and completed setup must reap their private Driver.
The synthetic foreground fixture must retain focus and all concurrent core keys.
Use the same compiled app test and mounts above, changing the final arguments to:

```sh
'^TestNativeLinuxDesktopSetupTUI$' /fixture.py
```

This covers the native X11 setup backend and terminal UI together in the isolated
container. It is not a physical terminal/IME test, public-network download test,
or evidence for macOS system authorization or Windows setup.

The negative native proxy check uses a verified App binary, temporary HOME and
an absent socket. It verifies that the proxy refuses automatic service launch,
without connecting to a user service or requesting OS permissions:

```sh
AICE_CUA_TEST_BINARY=/absolute/path/to/CuaDriver.app/Contents/MacOS/cua-driver \
  go test -tags=integration ./internal/desktop -run '^TestNativeCuaProxyRefusesAutolaunch$' -v
```

With the same binary, `TestNativeCuaStatusEstablishesAbsence` checks the pinned
read-only status diagnostic and public inspection API on a temporary absent
socket. It also uses an isolated HOME and does not launch a service. Default setup tests use fake
commands to cover lock contention, external restrictions, startup races and
partial authorization outcomes; they do not prove native grant behavior.

The metadata-only `TestNativeCuaSchemaInventory` uses that same explicitly
supplied binary with an isolated HOME and runs `dump-docs --type mcp`. It checks
the complete advertised schemas against the reviewed pin without constructing
a desktop runtime, enumerating windows, requesting grants or connecting to a
service. It requires a native macOS GUI environment because upstream CLI startup
initializes AppKit. Default raw MCP tests reject missing/changed schemas before
any tool call and keep additional upstream tools unavailable.

```sh
AICE_CUA_TEST_BINARY=/absolute/path/to/CuaDriver.app/Contents/MacOS/cua-driver \
  go test -tags=integration ./internal/desktop -run '^TestNativeCuaSchemaInventory$' -v
```

The desktop activity CLI check uses the actual interactive command, a scripted
model and synthetic typed backend, with isolated user settings. It exercises
application labels, waiting/planning, folded parameters and Settings Stop;
no native desktop or paid model is accessed:

```sh
go test ./internal/app -run '^TestDesktopActivityTUI$' -v
```

The macOS native manager acceptance test is separately opted in. It requires
the verified pinned App, an already running standard-mode service with its OS
grants, an available interactive desktop, and Xcode's Swift compiler. Preparation
only verifies installation and inspects the existing service; it never installs
or requests permissions. The subsequent task uses the production Manager,
including its ordinary lazy-start behavior if that service later disappears.
It opens three temporary AppKit target processes and a foreground sentinel,
performs semantic edits/commits with window screenshots, and checks independent
fixture state, fresh references, connection reuse and owned-session cleanup.
All nine images must retain verified capture mappings. Since the pinned macOS
projection omits passive labels, the returned editable value and independently
read post-response commit state are checked separately. It logs only operation
counts/timing and content-free diagnostics. Only these
synthetic windows receive actions; no model is called. Fixture processes and
temporary files are cleaned up on failure as well as success.

```sh
AICE_CUA_NATIVE=1 go test -tags=integration ./internal/desktop -run '^TestNativeCuaMultiApp$' -v
```

The macOS cross-toolkit gate transfers Unicode text through three synthetic
processes, AppKit → WebKit → AppKit, using one production Manager connection.
The embedded WebKit form uses a non-persistent store and local HTML with no
remote content. Its page handlers report DOM state only; all test input goes
through AICE/Cua. It checks independent input/commit results, nine returned
captures, exact label/role control selection, zero sentinel activation losses,
and owned-session cleanup with the shared service preserved:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacWebKitTransfer$' -v
```

This passed on 2026-09-27. AppKit uses `set_value`; the empty WebKit input uses
`type_text`. A prior attempt using WebKit `set_value` failed actual DOM readback,
as described in the [platform evidence](desktop.md#platform-evidence). The gate
does not downgrade that failed route to success or retry it automatically.
Run sequentially with other native focus gates. It requires the existing pinned
service and grants, makes no model calls and does not establish third-party
application, Electron, physical-input or actual-model compatibility.
The separate compilation-only gate opens no windows and connects to no service:

```sh
AICE_CUA_BUILD_FIXTURE=1 go test -tags=integration ./internal/desktop -run '^TestNativeMacWebKitFixtureBuild$' -v
```

The macOS cold-launch gate additionally creates and registers one unique
temporary AppKit bundle in `~/Applications`, a real Driver app-discovery root.
It discovers the unopened app, consumes one local app reference, checks its exact
bundle ID/PID, and explicitly selects the named fixture window if multiple
candidates are returned. It requires an actual capture and Unicode value/commit,
zero foreground-sentinel activation losses, rejection of the consumed launch
reference and continued application/shared-service availability after Manager
close. Its own cleanup requests fixture termination through a private file,
then unregisters/removes only the temporary bundle; no name/PID-wide kill is used.
This gate passed with race detection on 2026-09-27, with two returned candidates.
It needs a separate opt-in because it writes and registers a temporary app:

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_LAUNCH=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacLaunch$' -v
```

No Driver installation, new OS grants or model calls occur. This checks ordinary
AppKit launch, not self-activating third-party applications, physical input/IME
or other toolkits. Run it sequentially with the other focus-sensitive gates.

The macOS cursor lifecycle gate uses the production Manager for one pixel click
and the official read-only `sessions list --json` CLI for render acknowledgement.
It matches only its own public session label, keeps other session metadata out of
logs, and requires absent → hidden → visible → removed session/cursor states.
The fixture must commit once, the sentinel must retain focus and contents, and
the shared service must remain available. This passed with race detection on
2026-09-27 without an external UI observer:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacCursorLifecycle$' -v
```

For optional appearance inspection, set `AICE_CUA_NATIVE_CURSOR_HOLD_DIR` to a
fresh empty absolute directory. The test writes `ready` containing the exact
temporary App path; bind the observer, then create `act` to permit its one click.
After renderer acknowledgement it writes `acted`; inspect promptly before idle
fading and create `finish` to release cleanup. Each phase is bounded to four
minutes and the test to nine. The ordinary gate has no such wait, and neither
mode infers an appearance verdict from a marker. Inspect the Cua Driver's
transparent host surface: a target-only screenshot did not include its overlay
in the native probe. A blue cursor was visually observed on the host surface,
but the combined manual probes failed their focus assertions; retain those
failures separately from the passing automatic lifecycle gate. Desktop
compositing, animation, physical-pointer independence and multiple displays or
Spaces still require acceptance. No cursor preference is changed, no recording
is started, and no input is replayed to keep the cursor visible.

The separate macOS input gate checks ASCII and Unicode `type_text`, a single
`key`, and `cmd+a` through exact semantic element tokens. It reads the AppKit
field editor's actual text and selection after the response, including when
the Driver reports refusal or an unverifiable effect. Refusal or unmet input
postconditions fail the gate; a successful `set_value` seed does not count as
successful insertion. Each case has its own target and foreground sentinel,
which must retain focus and contents throughout setup, input and cleanup:

```sh
AICE_CUA_NATIVE=1 go test -tags=integration ./internal/desktop -run '^TestNativeMacInput$' -v
```

This gate uses the same read-only installed-service/grant preflight before
opening any fixture, installs nothing and requests no grants. All four cases
passed on the authorized macOS host on 2026-09-27, including independent full
selection verification when the Driver returned `effect:unverifiable` for the
hotkey. The sentinel detects activation loss and misdirected text; it does not
generate physical or IME input. Pixel actions,
other toolkits, overlay and real user coexistence remain separate acceptance.

The separate macOS pixel-click gate measures the AppKit button center and
window frame independently, converts that geometry into coordinates in the
actual returned image, and dispatches through the production Manager. It checks
one real commit, then separately resizes the window and requires
`capture_frame_mismatch`/`refused` with zero commits. Only a newly returned image
and newly calculated point may complete the second case. Both cases verify the
foreground sentinel through connection cleanup and preserve the shared service:

```sh
AICE_CUA_NATIVE=1 go test -tags=integration ./internal/desktop -run '^TestNativeMacPixelClick$' -v
```

Both cases passed on the authorized macOS host on 2026-09-27. The initial
500×328-point window produced a 1000×656-pixel image. Independent widget state
confirmed the click even though the Driver reported `effect:unverifiable`.
The resized 900×378-point frame produced a 1600×672-pixel image and its newly
calculated point committed once, covering Driver downscaling on this Retina host.
This is screenshot-coordinate acceptance on the AppKit fixture, not visual model
recognition, crop/negative-monitor geometry, scrolling, dragging or overlay QA.

The separate window-translation gate captures at screen x=100, moves the
synthetic window to x=−40 while preserving size/content, then clicks using the
original screenshot-local coordinates. Independent AppKit state must show one
commit, one native click request, the retained negative origin and no sentinel
focus loss. It also requires a fresh bound screenshot and a usable shared
service after cleanup. Pure translation keeps window-local coordinates valid;
it is not the resize-refusal case. Run sequentially with other native gates:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacWindowMove$' -count=1 -v
```

This passed on the authorized macOS 0.29.1 host on 2026-09-27. It uses one display
and a partly off-screen window; multiple monitors, mixed display scales and
image crops remain separate acceptance. No model or user application receives
the synthetic input.

The separate pointer-button gate uses a custom AppKit view without AXPress or a
context menu. It counts actual left/right down/up events, checks click count,
window/button/modifiers and screenshot-derived position, and requires exactly
one native `click` RPC per request. It checks zero sentinel activation losses
before dispatch and through cleanup. Both cases currently **fail** on macOS
0.29.1: double-click delivers the correct pairs but briefly loses sentinel focus;
right-click delivers duplicate pairs. The strict acceptance conditions remain:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacPointerButtons$' -count=1 -v
```

Run sequentially with other native focus gates. It uses temporary synthetic
windows, existing installation/grants and no model calls. The default suite
skips it. See the [platform evidence](desktop.md#platform-evidence) for measured
results and the distinct source-based explanation; semantic menu invocation and
physical input are not covered.

The macOS gesture gate uses an actual `NSScrollView` and `NSSlider`, their
independent geometry and post-response widget state. Background scroll passed
on 2026-09-27 (offset 0→60); background drag remains a failing postcondition
because the pinned Driver returns `background_unavailable` and leaves the
slider at zero. Both cases preserve the foreground sentinel. Keep the failing
native case explicit; the default suite skips these opt-in desktop actions:

```sh
AICE_CUA_NATIVE=1 go test -tags=integration ./internal/desktop -run '^TestNativeMacGestures$' -v
```

Foreground drag has an additional opt-in because it can affect the real pointer
and temporarily activate the synthetic target. This gate binds the existing
`foreground_allowed` mode, verifies background refusal without input/focus loss,
rejects the consumed observation, and then explicitly dispatches from the fresh
image. One run completed with slider value 0→92.7 and one observed activation
loss followed by sentinel-focus restoration. A later run passed the strengthened
pre-foreground focus check but failed movement/restoration with ChatGPT
foreground. The current native gate is therefore not consistently accepted;
keep that failed postcondition visible. It checks unaffected controls and
shared-service cleanup without asserting background coexistence or changing
saved settings:

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_FOREGROUND=1 go test -tags=integration ./internal/desktop -run '^TestNativeMacForegroundDrag$' -v
```

These fixture tests do not establish physical user/IME coexistence, heterogeneous
application dragging, snapshot geometry changes during scroll/drag, or overlay
appearance. They do not silently switch a background-only run to foreground.

The native cancellation gates use the production Manager and synthetic AppKit
targets without a foreground sentinel. One cancels a condition wait after a
completed native poll while another window's click competes for execution; it
requires zero click dispatches and permits input only through a fresh run and
observation. The other independently observes a committed click before its RPC
returns, then cancels and requires retained dispatch status, no replay and fresh
read-only recovery. Both reject old references and verify the shared service
remains usable. They passed with race detection on 2026-09-27; the in-flight
case reported `unknown` and retired its task connection. Run sequentially:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacCancel(Wait|DispatchedClick)$' -v
```

These gates count actual native calls and inspect independent widget state.
They do not establish foreground coexistence, native TUI Stop, interrupted
gestures or cleanup of every resource inside the Driver. A complete native reply
that races cancellation remains a known result; cancellation must not rewrite it
as unknown. No cancelled mutation is replayed during recovery.

The macOS Settings Stop gate uses the actual command, Loop, Guard, typed tools,
production Manager and Session with a scripted model. It discovers and captures
one exact synthetic AppKit window, then cancels its native condition wait through
Settings-local F6. Esc alone must keep the wait running. The test checks that
polling started, cancellation precedes one binding cleanup, no model continuation
or widget mutation occurs, saved preferences remain unchanged, and all three
tool pairs plus the capture survive Session replay. The shared service must
remain usable after command exit. This passed with race detection on 2026-09-27:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacDesktopStopTUI$' -v
```

Its terminal input is piped and it uses no foreground sentinel. This is native
condition-wait cancellation through the real UI, not physical keyboard/IME,
foreground coexistence or Stop during a native mutation. Setup reuses existing
grants and installation; it installs nothing and requests no new permissions.

The separate mutation variant opens Settings before releasing the scripted
model's click decision, then waits for independent widget state to prove one
commit. It presses Settings F6 while the native result is still pending; native
input and responses are never held by the harness. It requires a dispatched
`unknown` outcome, exact result retention in Session replay, one commit and
binding cleanup, unchanged saved preferences, no model continuation, and a
usable shared service. A result that already returned cannot pass this gate.
This passed with race detection on 2026-09-27, showing cancellation in 1.11 s:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacDesktopStopMutationTUI$' -count=1 -v
```

Run it sequentially with other native gates. It uses the existing verified
service and synthetic AppKit fixture, no real model or new grants. This proves
Settings Stop during one committed click's pending response; it does not prove
physical keyboard input, continuous focus or interrupted gesture cleanup.

The application-level macOS gates use synthetic AppKit targets, with a variant
substituting the middle target with the local WebKit form. They use a scripted
model through the actual print command, Guard, typed tools, production
desktop constructor and Session writer. They check three exact-window Unicode
value changes/commits, nine PNGs delivered directly to later model requests,
exact replayed tool results/images and stable message parents. Configuration and
skill discovery use temporary directories; only desktop resolution uses the
host's verified App and service endpoint. It installs nothing and requests no
permissions during preparation. As in the Manager test, the run retains ordinary
lazy service-start behavior if the admitted service later disappears.
They also check that text progress omits native input bodies, the armed sentinel
never loses focus, and command cleanup leaves the shared service available:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app -run '^TestNativeMac(DesktopPrint|WebKitPrint)$' -v
```

The WebKit variant uses `type_text` on the empty web input and matches both
label and role. Its actual page state must contain the requested Unicode value
and one commit, while the Driver's `unverifiable` effect must reach the model and
survive exact Session replay. It passed with race detection on 2026-09-27 in
24.55 s of command execution, with 11 scripted model requests, nine PNGs and no
sentinel focus loss. The AppKit-only gate passed in the same sequential run.
Both retain isolated configuration/skills and existing grants; neither invokes
a real model or establishes third-party browser/profile compatibility.

The shared model/Session checks are also used by the native Linux print gate.
Both the Manager and CLI gates passed on the authorized macOS 0.29.1 host on
2026-09-27. The CLI task completed its three commits in 19.53 s with nine PNGs
replayed and no sentinel focus loss. Earlier attempts encountered foreground
loss; subsequent passes do not identify its cause or prove physical-user
coexistence. Run these native GUI gates sequentially on an available desktop;
parallel fixtures would invalidate their focus assertions. A separate compilation-only
check opens no windows, connects to no service and requests no grants:

```sh
AICE_CUA_BUILD_FIXTURE=1 go test -tags=integration ./internal/app -run '^TestNativeMacPrintFixtureBuild$' -v
```

The separate macOS Settings reuse gate invokes the production public
grant/direct-capture flow through CLI/Bubble Tea. It requires the verified App,
existing grants and running service before starting; downloads are disabled.
It checks cancellation before external work, then explicitly confirms repair,
verifies live capture, saves enable into temporary configuration, and checks
that no model request or Session was created and the shared service survives.
Unlike task gates, this can show OS permission UI and probe direct screen
capture; it has its own opt-in and does not establish first-time installation
or physical interaction with system dialogs. After asynchronous Settings status
loading was integrated, this gate passed with the race detector on the authorized
0.29.1 host on 2026-09-27 in 10.47 s:

```sh
AICE_CUA_NATIVE_SETUP=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacDesktopSetupTUI$' -count=1 -v
```

The sentinel counts activation loss notifications while armed; returning to it
at the end cannot erase a temporary focus loss. It does not inject a stream of
global keystrokes and does not replace physical keyboard, native IME, overlay,
pixel-action or heterogeneous-app acceptance. The CLI gate also uses a scripted
model, not actual-model visual reasoning. Run without unrelated
foreground changes. A foreground login window fails the opt-in fixture check;
the test never tries to unlock it. Three copies of the AppKit fixture do not
establish compatibility with Electron or other native toolkits.

Two narrower opt-ins validate the harness separately. The first only compiles
and opens no windows. The second opens/closes the four synthetic windows and
checks the focus monitor without connecting to Cua:

```sh
AICE_CUA_BUILD_FIXTURE=1 go test -tags=integration ./internal/desktop -run '^TestNativeCuaFixtureBuild$' -v
AICE_CUA_TEST_FIXTURE=1 go test -tags=integration ./internal/desktop -run '^TestNativeCuaFixtureLifecycle$' -v
```

See the [platform evidence](desktop.md#platform-evidence) for actual results;
the presence or compilation of an opt-in test is not native acceptance.

### Native session idle expiry

The macOS expiry gate waits for the official daemon's default five-minute
session idle timeout and thirty-second maintenance sweep. It requires a separate
opt-in because it takes more than five minutes. It does not alter TTLs, call
private lifecycle APIs, inject an expiry response or restart the shared service:

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_EXPIRY=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacSessionExpiry$' -count=1 -timeout=9m -v
```

The fixture captures one exact synthetic AppKit window, then sends no task
traffic while a separate read-only operator connection watches that session's
label disappear. Disappearance before 290 seconds or no disappearance within
six minutes fails the gate. An action using the old semantic token must fail
without changing the fixture; the test preserves either a returned native error
or an unknown transport outcome and never replays the consumed reference.
After closing that run, a new run must discover/capture the window and commit
once using a new token. The shared service must remain usable after cleanup.
This does not test daemon restart, permission revocation, physical input or
continuous foreground focus; it has no foreground sentinel. Run sequentially
with other native gates. Default tests skip it and do not wait for native expiry.

This passed with race detection on macOS 0.29.1 on 2026-09-27: expiry was observed
after 5 min 20 s, the old action returned `outcome:returned` with `driver_error:true`
and no observation or widget commit, and recovery used a second connection to
commit exactly once. The full gate took 335.95 s. It first reproduced a recovery
failure where a live MCP pipe retained expired native lifecycle state. Failed
discovery or an unusable target observation now retires that connection without
retrying input; returned action details remain intact. Offline tests cover app
and window discovery failures, invalid observations and failed post-action
observations, while preserving valid partial semantic observations.

Keep the desktop unlocked for the full interval. The gate checks for
`loginwindow`, including before the expired action, and never unlocks the host
or changes its lock policy. An earlier locked-host attempt was incomplete;
session disappearance alone does not satisfy this gate.

### Native owned-proxy crash

The separate crash gate terminates only the MCP proxy child it created through
the production transport. Shared-service admission and the AICE occupancy lock
remain active. It first observes a synthetic AppKit Commit through independent
widget state while the native RPC response is still pending, then kills that
exact child handle. A completed response cannot satisfy this precondition.

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_PROXY_CRASH=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacProxyCrash$' -count=1 -v
```

On macOS 0.29.1 this passed with race detection on 2026-09-27 in 8.04 s. The
pending action settled in 2.10 ms as dispatched/unknown without an observation.
Old references were refused both in the original run and a replacement run.
Explicit read-only discovery/capture established a second admitted connection;
the native click count and independent widget commit count both remained one.
The public standard-mode service status retained the same daemon PID. This
tests proxy-process loss, not daemon restart, interrupted drag/key release,
permission revocation or foreground/IME coexistence. It requests no grants or
model calls and must run sequentially with other native GUI gates.

### Native same-run reconnection

The metadata-only macOS gate verifies reconnection inside one AICE run:

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_RECONNECT=1 \
  go test -race -tags=integration ./internal/desktop \
  -run '^TestNativeMacSameRunReconnect$' -count=1 -v
```

It discovers app/window metadata, explicitly retires only AICE's connection,
then discovers again in the same run. It performs no capture, input, activation
or model request. Production admission and occupancy remain in use, and the
shared daemon identity is checked before and after. Run sequentially with other
native gates and interactive desktop tasks; default tests skip this gate.

On 2026-09-27 this reproduced `Driver session unavailable`: AICE reused the
old native lifecycle label on a new transport. Cua's owner checks reject that
claim. Issuing a fresh label per native session start made the gate pass with
race detection in 4.43 s. Offline unusable-read tests also model the native
ownership rejection and verify fresh discovery/action, invalid old references
and no replay across app, window, observation and post-action failures. This
proves recovery after connection retirement, not the cause of the manual run's
initial discovery failure or physical foreground behavior.

### Native discovery idle recovery

The discovery-idle gate separates the pinned Driver's implicit discovery
lifecycle from AICE's explicitly named action lifecycle:

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_DISCOVERY_EXPIRY=1 \
  go test -race -tags=integration ./internal/desktop \
  -run '^TestNativeMacDiscoveryIdleRecovery$' -count=1 -timeout=9m -v
```

It first discovers metadata, then waits six minutes without further discovery.
Every thirty seconds it redeclares only its existing explicit session through
public `start_session`, checking that it remains active and was not revived.
This is test-only activity, not a production keepalive. No TTL override, private
session field, capture, input, app launch, focus change or model call is used.
The public `list_apps` and `list_windows` schemas accept no session argument.

After the wait, discovery must either remain usable or return a native
`session_ended` error that retires the connection and old references. In the
latter case, the next explicit discovery must succeed in the same AICE run
using a fresh native identity. Other errors fail the test. The shared daemon
identity must remain unchanged. Run sequentially with other native gates and
interactive desktop tasks; default tests skip it. This gate verifies metadata
discovery recovery, not a gesture, physical focus or model task.

On macOS 0.29.1 on 2026-09-27, this passed with race detection in 364.65 s.
After six minutes the explicit lifecycle was still active without revival, but
the daemon rejected `list_apps` because its implicit session had ended. The
proxy's structured code was `tool_invocation_failed`; the native text identified
the ended session. The test recognizes that daemon form and the core's nested
`refusal.code` form without logging the session identity. Production recovery
does not match either error text or code. The next explicit discovery admitted a
new connection and lifecycle in the same AICE run, and shared-service identity
was preserved. No input, capture or model call occurred.

The manual Session's 468.432-second gap between successful discovery and failure
is consistent with this independently reproduced path. Its generic tool error
did not retain the underlying native reply, so this is supporting evidence,
not proof of that historical call's exact cause. The implicit lifecycle can
expire even while explicitly named actions continue; a first discovery after
such an idle interval may fail and require a new read. Do not replay prior input.

### Explicit real-model desktop gate

`TestNativeMacActualModelDesktop` uses the selected production provider, Agent
Loop, Guard, typed desktop tools, Manager and Session recorder for the same
three synthetic forms (AppKit/WebKit/AppKit). The model chooses its actions;
independent widget/DOM readback requires the assigned Unicode text and exactly
one commit per window. Each window must have been captured, returned images
must reach model requests, exact tool-result/image replay must pass, and the
sentinel and shared service must survive cleanup. This is a targeted Loop/model
gate, not the full CLI with a real model, physical input or third-party apps.

The harness registers only the three desktop tools. Its test-only scope wrapper
admits the exact synthetic discovery query, removes unrelated discovery entries,
and refuses references outside those windows, foreground delivery, launch and
non-task keyboard actions before dispatch. It retains genuine native results;
this is not a product allowlist or a change to Cua standard mode. Scope refusals
fail acceptance even if a model later completes the task. Unknown mutations are
not replayed by the harness. The guard remains the production gate without yolo;
any application approval also fails acceptance.

First verify this harness without provider access:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacModelHarness$' -v
```

That scripted-decision check passed on 2026-09-27 with 11 requests, nine images,
zero Guard asks and zero scope refusals. The real-model gate has a separate
opt-in; neither `AICE_CUA_NATIVE=1` nor the integration build tag enables it.
After selecting and authorizing a provider/model, set all of the following:

```sh
AICE_CUA_NATIVE_MODEL=1 \
  AICE_CUA_MODEL_PROVIDER='<configured-provider-id>' \
  AICE_CUA_MODEL_ID='<image-capable-model-id>' \
  AICE_CUA_MODEL_THINKING='<supported-thinking-level>' \
  AICE_CUA_MODEL_ARTIFACT_DIR='/absolute/fresh-empty-directory' \
  go test -race -tags=integration ./internal/app -run '^TestNativeMacActualModelDesktop$' -count=1 -v
```

This explicitly reads normal user configuration/credentials and uses the normal
provider authentication path, including OAuth refresh if applicable. It does
not read project settings or skills. Provider selection and thinking are
required rather than silently using an ambient default. The model receives only
the synthetic task and admitted native observations; credentials stay outside
the prompt and logs. It can consume provider quota or incur charges.
The run allows at most 20 model attempts and five minutes, with 4,096 requested
output tokens per response. Its default reported-token budget is 100,000.
After separately authorizing a different budget, set
`AICE_CUA_MODEL_TOKEN_BUDGET` to an integer from 1 through 10,000,000. Invalid or
empty supplied values fail before configuration or credentials are read; zero
cannot enable an unlimited run. This variable does not authorize another run
and is ignored by the scripted harness. Token limits are checked between
operations, include reported cache usage, and are not an exact billing ceiling.

The caller must create a fresh empty artifact directory. `task.txt`, the normal
`model-task.jsonl` and `report.json` remain there for review once the run reaches
those stages. The report separates Loop completion from full `accepted` status;
it contains counts/usage and the effective request/token/time/output limits,
not credentials or input bodies. The Session does
contain the synthetic images and model transcript. Default tests skip all
provider reads/calls; offline scope tests reject out-of-scope actions and stale
references without a native backend call. Preparing or passing the scripted
harness does not establish real-model acceptance.

One explicitly authorized run on 2026-09-27 used
`opencode-go/muse-spark-1.3-contributor` with `xhigh` and Driver 0.29.1 on
macOS arm64. It stopped at the reported-token budget after 96.26 s of Loop
execution: six requests, six images delivered to model requests, zero Guard
asks and zero scope refusals. Usage was 101,745 tokens, including 68,661 cache-read
tokens. The budget is checked between operations, so the sixth response could
cross 100,000; its requested first Commit click received a budget refusal without
native dispatch. Before that, three input actions returned native results and
images. No Commit was dispatched. The independent final widget/DOM assertions,
sentinel assertion, shared-service reinspection and exact replay acceptance were
not reached. The report correctly records `loop_completed=false` and
`accepted=false`; this is an incomplete attempt, not real-model acceptance.
The run's Session, task and report were retained in the caller-selected artifact
directory. Further runs must stay within the operator's authorized model and
budget scope; an existing authorization need not be requested again.

After the operator authorized a 10,000,000-token envelope and both models on
2026-09-27, two further real-provider attempts ran sequentially with the same
three-form task and race detection:

| Model / thinking | Loop time | Requests / images | Reported tokens | Result |
| --- | --- | --- | --- | --- |
| `muse-spark-1.3-contributor` / `xhigh` | 111.64 s | 9 / 4 | 126,761 | The model serialized the entire action object into the `action` string; three scope refusals prevented dispatch. The model ended, but widget postconditions failed. |
| `deepseek-v4.1-flash` / `high` | 81.13 s | 11 / 9 | 195,698 | All three independent widget/DOM values and exactly-one-commit assertions passed, with zero Guard asks/scope refusals. The final foreground sentinel assertion failed. |

Both reports have `accepted=false`; Loop completion alone is insufficient.
The DeepSeek failure does not identify whether Driver behavior or an external
foreground switch caused the sentinel failure. Shared-service reinspection and
exact Session replay assertions after that check were not reached. The two runs
consumed 322,459 reported tokens in the newly authorized envelope.

After adding explicit action-shape guidance and rejecting invalid action names
at the typed tool boundary, another `muse-spark-1.3-contributor` / `xhigh` run
completed all three widget/DOM postconditions with exactly one commit each.
It took 131.03 s of Loop time, 14 requests, 12 images and 352,372 reported tokens,
with zero Guard asks and zero scope refusals. The foreground sentinel assertion
still failed, so `accepted=false`; subsequent shared-service/replay checks were
not reached. This sample does not prove that prompt changes alone caused the
model's corrected behavior or that focus interference is attributable to Cua.
The three attempts total 674,831 reported tokens in the 10,000,000-token envelope.
The operator subsequently reported that the [manual checks](desktop-manual-checks.md#当前验收结果)
passed except first-time installation, including physical input/focus and a
TextEdit → Safari → VS Code transfer with one form submission. This is operator
evidence for that run, not a replacement for the failed automated sentinel
assertions. The retained manual Session subsequently established an initial
discovery failure followed by same-run recovery failure; the latter was
reproduced and fixed by the native reconnect gate above. The initial failure's
cause remains unverified. The [manual record](desktop-manual-checks.md#当前验收结果)
adds its 3,746,474 reported tokens, bringing usage in the authorized envelope to
4,421,305. The three automated attempts alone account for the 674,831 above.

The harness also retains sanitized sentinel samples before/after each tool,
before/after the Loop and after cleanup: elapsed time, fixture tick, tool
sequence, activation state, loss count, foreground category (sentinel, task
target, other, unknown or loginwindow), baseline-value match and synthetic-task
value category. It records no foreground PID, application name or input text.
The fixture samples every 50 ms, so consecutive event-boundary reads can share a
tick; these records locate observed changes but do not establish causation.
Focus failure still fails acceptance, while independent shared-service and exact
Session replay checks now continue and record their own verification flags.

Two scripted diagnostic runs on 2026-09-27 failed the focus assertion. The first
recorded three losses and a changed sentinel value. The second completed the
three widget/DOM postconditions in 23.63 s with 11 scripted requests, nine images,
zero Guard asks and zero scope refusals. Its first recorded focus loss was after
tool 4 (the first Commit), around 9.10 s; foreground was outside the fixture
targets, and the sentinel value stayed unchanged. Shared-service verification
and exact tool-result/image replay both passed. The operator then confirmed
switching windows or typing during these two runs. Their focus measurements
are contaminated by concurrent human activity and establish neither Cua-caused
focus loss nor a passing sentinel gate. No paid model run followed this baseline.
This clarification applies to these two diagnostic runs, not all earlier failures.

The report also records platform, architecture, Driver, actual/scripted model
transport and per-request/tool timing samples. Model time starts immediately
before invoking the provider and ends at its terminal event or stream error;
`first_tool_call_ms` is present only when a complete tool-call event arrives.
This includes provider encoding, transport and local stream handling, not just
remote inference. Stream failures remain failures and are retained as attempts.
`preparation_gap_ms` measures the interval from the preceding completed Loop
turn to the next provider invocation, including Loop bookkeeping and the test's
image accounting. It is absent without that preceding boundary; it is not a
pure request-encoding microbenchmark and does not isolate network latency.

Tool totals span execution-start through execution-end, including Guard and
Session result persistence. Guard check and optional revalidation have separate
samples. Admitted actions include the existing Manager timings for total, queue,
Driver round trip, condition wait and final observation/image processing.
These phases are nested, not additive across request/tool/action totals. A zero
wait duration means no condition wait occurred; omitted stream/Guard fields
mean their measurement boundary was not reached. Tool/request sequence numbers
identify first and subsequent calls; fixture setup is outside Loop elapsed time,
the shared service is already running and OS/provider caches are not controlled.
Do not label this a cold process-start benchmark.

The scripted native timing gate passed with race detection on 2026-09-27:
11 requests, 10 tools, six actions, nine images and zero approvals/scope refusals.
Its Loop took 23.35 s; sampled Driver round trips were 2.40–3.28 s and final
observations 0.35–0.39 s. This is one instrumented acceptance run, not a speedup,
statistical performance comparison or actual-model result. The scripted gate
also writes/reads the report in its temporary directory. Timings remain outside
model messages and Session JSON; they contain no input values, window identities,
credentials or images. Offline stream tests preserve events, EOF/terminal
semantics and Close errors through the measurement wrapper.

The incomplete real-model run above retained six provider timing samples totaling
83.02 s and seven executed tool samples totaling 13.05 s. Its three dispatched
input actions sampled Driver round trips of 1.94–3.20 s and final observations
of 0.36–0.38 s. This single race-instrumented sample includes provider transport
and does not isolate network time, prove task completion, or establish controlled
cold/warm performance.

## Browser checks

The default browser/dependency tests use fake commands and local HTTP fixtures,
not downloads or user profiles. Fake executable lookups must return host-absolute
paths for installed helpers, including Git Bash, so Windows discovery does not
trigger unrelated provisioning during browser tests.
The opt-in native test requires the verified
pinned helper plus installed Chrome/Chromium/Brave:

```sh
AICE_BROWSER_TEST_HELPER=/absolute/path/to/agent-browser \
  go test -tags=integration ./internal/browser -run '^TestNativeManagedBrowserLifecycle$' -v
```

It checks headed and headless modes (the headed case requires a desktop display),
using isolated sessions, a local data-URL form, snapshots, input actions,
screenshot PNG decoding, close/sidecar checks and a fresh generation. Never point
it at the user's live profile. Actual `/browser` TUI, external CDP connection and
Chrome Allow acceptance need a dedicated profile and a desktop. Keep native,
scripted-model and actual model evidence separate in the [acceptance matrix](browser.md#maintenance-and-verification).
