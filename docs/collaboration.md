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

The [CI](../.github/workflows/ci.yml) and [release](../.github/workflows/release.yml)
workflows share [verify.yml](../.github/workflows/verify.yml): Linux/macOS/Windows,
the Go version from `go.mod`, ripgrep, race tests, vet and offline installer tests.
CI runs on `main` pushes and pull requests. Release publication requires both
verification and builds, and occurs only on `v*` tag pushes; manual dispatch
builds bundles without publishing. Only the publishing job receives write access.

Windows uses `go test -race -p 1 -parallel 2 ./...` to bound process pressure
without dropping tests or race detection. Preserve sequential real-ripgrep
acceptance where concurrent child processes exhaust hosted-runner resources.
A local pass establishes only that platform.

For macOS clipboard changes, use an isolated pasteboard:

```sh
go test -tags=integration ./internal/tui -run '^TestMacClipboardNativeFormats$'
```

Default tests use synthetic clipboard data and bounded helpers. Linux/Windows
clipboard and physical IME behavior require their native desktops. CLI-driven
TUI tests must wait for the expected reply and idle frame, not just an earlier
command's idle header; resize repaints can expose full asynchronous frames.

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

Default tests use temporary homes/workspaces, synthetic credentials and local
services. Do not remove real execution or disk readback merely because a nearby
unit test has similar inputs. Key coverage:

| Path | Verification |
| --- | --- |
| Built binary Print | [main_process_test.go](../cmd/aice/main_process_test.go): stream separation, exits, limits, secret-file deny under `--yolo` |
| Incomplete/failed model streams | [stream_failure_test.go](../internal/agent/stream_failure_test.go): no execution of unaccepted deltas; retained calls remain paired |
| TUI output | [terminal_rendering_test.go](../internal/tui/terminal_rendering_test.go): actual Bubble Tea output, separate from native terminal/IME acceptance |
| Configuration and credentials | Config lock/replacement tests: cancellation, partial failure, concurrent writers/refresh and native Windows sharing conflicts |
| Long tasks | [long_task_test.go](../internal/app/long_task_test.go): 200 model rounds, real app compaction, pairing, budgets and failure recovery |

Run the long-task subset with:

```sh
go test ./internal/app -run '200ModelRounds|CompactionFailureBoundaries' -v
```

The [Go service](../evals/go-service/README.md) and [Python CLI](../evals/python-cli/README.md)
evaluation families have separate specifications and self-checks outside the root
Go suite. Scripted models prove harness behavior; reference fixtures prove task
and fault detection; actual model runs measure generated-code quality. Keep
these claims separate. For model comparisons retain settings, interventions,
initial/final source and refactor-only diffs; agree on models and cost before
paid evaluation.

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

## Real MCP interoperability

[MCP verification](mcp.md#verification-evidence) owns the external service commands
and platform limits. Run its offline driver before opt-in interoperability:

```sh
go test -race -tags=integration ./internal/app -run '^TestMCP(PrintInteropHarness|InteropModelSelection)$' -v
```

Filesystem, DeepWiki and account OAuth gates need their explicit switches;
an integration build alone must not launch third-party services, read normal
credentials or contact them. Use isolated settings and temporary data, preserve
schema-bound grants, and distinguish scripted interoperability from real-model
task completion. Account access requires explicit authorization.

## MCP retrieval checks

The deterministic corpus in `internal/app/testdata/mcp-search-tasks.json` checks
50 natural-language requests against 50/500/2,000 tools. It uses no model,
credentials or external services. Run it when changing ranking:

```sh
AICE_MCP_SEARCH_EVAL=1 go test ./internal/app -run '^TestMCPCatalogRetrievalEvaluation$' -count=1 -v
```

Natural-language misses are reported separately from exact-ID correctness.
The default suite retains focused lexical, pagination, identity and selection
regressions. Generated reports, model transcripts and run logs belong outside
the repository; they are not test fixtures. Do not add batch runners or tests
of report accounting to the product suite.

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

Compare time/allocation per operation separately from retained memory. These
synthetic fixtures cover warm catalog queries, text matching, first-view rendering
and history navigation; they are not terminal latency benchmarks.
`TestSessionBrowserTUI` separately exercises search, preview, read-only viewing
and resume through the CLI with generated history and isolated settings.

## Git and Collaboration

- Multiple sessions may share this worktree. Preserve unrelated staged, unstaged, and untracked changes.
- Modify and stage only explicit files owned by the current task. Never use `git add .`, `git add -A`, `git stash`, `git reset --hard`, `git checkout .`, or force push.
- Never commit unless the user asks. Before committing, inspect `git status` and the exact staged diff.
- When the user asks for incremental commits, make one independently verified commit after each completed small step.
- When asked to commit, follow the repository's existing short gitmoji/conventional subject style and keep each commit to one intent. Do not use Pi package scopes or release conventions.
- If a conflict touches a file not modified for the current task, stop and ask the user instead of resolving it speculatively.

## Computer Use checks

Default tests use fake peers, synthetic images and temporary configuration; they
perform no native desktop or provider calls. Integration compilation alone does
not run the opt-in gates. Native evidence and unresolved failures live in
[Computer Use](desktop.md#platform-evidence); keep historical results separate
from current-version acceptance.

Run native GUI gates sequentially on an unlocked test desktop without unrelated
foreground changes. They require an already verified pinned runtime/service and
existing grants unless explicitly testing installation/setup. They act on
synthetic fixtures, not user applications. Native actions, OS permission UI and
paid model calls require authorization for that scope; an existing authorization
remains valid. Never turn a failed input postcondition into a passing refusal test.
Keep generated reports, captures and Sessions outside the repository.

### Artifacts and read-only admission

Use already downloaded archives; these checks do not download releases. The
artifact-only tests do not execute desktop actions or grant OS permissions.

```sh
AICE_CUA_TEST_ARCHIVE=/absolute/path/to/cua-driver-rs-0.30.4-darwin-universal.tar.gz \
  go test -tags=integration ./internal/deps -run '^TestNativeCuaArtifactExtraction$' -v
AICE_CUA_TEST_ARTIFACTS=/absolute/path/to/archives \
  go test -tags=integration ./internal/deps -run '^TestCuaNativeReleaseArchives$' -v
```

The first checks macOS extraction, signing/Gatekeeper and publication without
installing. The second checks all four Linux/Windows archives and native-file
hashes without execution or signature-trust acceptance. On native Linux/Windows,
the private-install gate additionally runs native version/signature checks and
publishes/reuses a test-owned directory, without starting a daemon:

```sh
AICE_CUA_TEST_NATIVE_ARCHIVE=/absolute/path/to/native-archive \
  go test -tags=integration ./internal/deps -run '^TestNativeCuaPrivateInstallation$' -v
```

On macOS, absent-socket probes use an isolated HOME and must not launch a service.
The inventory uses `dump-docs --type mcp`, which initializes AppKit but does not
connect, enumerate or capture:

```sh
AICE_CUA_TEST_BINARY=/absolute/path/to/CuaDriver.app/Contents/MacOS/cua-driver \
  go test -tags=integration ./internal/desktop \
  -run '^TestNativeCua(ProxyRefusesAutolaunch|StatusEstablishesAbsence|SchemaInventory)$' -v
```

Windows status requires the installed pinned distribution and an existing
standard-mode service from that binary/user/login session. It only inspects and
leaves the service running; it is not action or secure-desktop acceptance:

```powershell
$env:AICE_CUA_NATIVE_STATUS = '1'
go test -tags=integration ./internal/desktop -run '^TestNativeWindowsServiceInspection$' -v
Remove-Item Env:AICE_CUA_NATIVE_STATUS
```

In a disposable Linux container without a user display/bus, headless inspection
starts/reaps only its test-owned daemon:

```sh
AICE_CUA_HEADLESS_CONTAINER=1 AICE_CUA_TEST_BINARY=/absolute/path/to/cua-driver \
  go test -tags=integration ./internal/desktop -run '^TestNativeLinuxHeadlessInspection$' -v
```

### Linux X11 fixtures

The runner installs Xvfb/Openbox/GTK/AT-SPI only inside the disposable container
and runs as an ordinary user with private display/D-Bus. Do not run its package
preparation on the host or mount host display, bus, home or input devices. The
binary, archive and container must use the same native architecture; emulation
and cross-compilation do not establish native execution. Python/GTK is a test
fixture, not an AICE runtime dependency.

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags=integration \
  -o /tmp/aice-desktop-linux.test ./internal/desktop
docker run --rm \
  --mount type=bind,src=/tmp/aice-desktop-linux.test,dst=/probe.test,readonly \
  --mount type=bind,src=/absolute/path/to/cua-driver-rs-0.30.4-linux-arm64.tar.gz,dst=/driver.tar.gz,readonly \
  --mount type=bind,src="$PWD/internal/desktop/testdata/run-linux-probe.sh",dst=/run-probe.sh,readonly \
  python:3.13-slim sh /run-probe.sh /probe.test /driver.tar.gz
```

The default runner checks native background/Manager behavior and the focus
sentinel through owned and shared runtimes. Explicit independent widget state,
native call counts, PNGs, session/child cleanup and shared-service survival are
the evidence. Read-only inspection between observation/input must preserve the
native token. Optional third arguments select additional gates:

| Selector | Boundary |
| --- | --- |
| `^TestNativeLinuxInput$` | ASCII/Unicode insertion, keys, pixel click/resize, scroll and drag; [known failures](desktop.md#linux-input-acceptance-failures) remain failures |
| `^TestNativeLinuxLaunch$` | Test-owned XDG app launch/PID/window, one follow-up task, application survival; [focus failure](desktop.md#linux-launch-acceptance-failure) remains open |

For actual CLI/Session or setup/TUI coverage, compile the app package and mount
the synthetic application fixture:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags=integration \
  -o /tmp/aice-app-linux.test ./internal/app
docker run --rm \
  --mount type=bind,src=/tmp/aice-app-linux.test,dst=/probe.test,readonly \
  --mount type=bind,src=/absolute/path/to/cua-driver-rs-0.30.4-linux-arm64.tar.gz,dst=/driver.tar.gz,readonly \
  --mount type=bind,src="$PWD/internal/desktop/testdata/run-linux-probe.sh",dst=/run-probe.sh,readonly \
  --mount type=bind,src="$PWD/internal/desktop/testdata/linux-fixture.py",dst=/fixture.py,readonly \
  python:3.13-slim sh /run-probe.sh /probe.test /driver.tar.gz \
  '^TestNativeLinuxDesktopPrint$' /fixture.py
```

Print uses a scripted model but production configuration, Guard, managed tools,
installer/runtime and Session. It requires three exact Unicode values/commits,
nine PNGs in model requests and replay, complete parents/tool pairs, no sentinel
focus loss and private-child cleanup. Text progress must omit input bodies.
Replace the last selector with `^TestNativeLinuxDesktopSetupTUI$` for disclosure,
default Cancel, retained installation, explicit window selection/capture and saved
enablement through the real TUI, with no model/Session. Local archives are served
through an in-memory transport; neither verifies public downloads or physical IME.

### macOS fixture gates

These gates need the pinned App, running standard-mode service, existing grants,
an unlocked desktop and Swift compiler. Ordinary task preparation does not install
or request grants. Production lazy launch remains possible if the admitted service
later disappears. Each native operation uses `Tools`/`CallChecked`; fixture-only
polling/geometry/postcondition helpers are not production semantics.

Compilation-only and fixture-only checks are separate from Cua admission:

```sh
AICE_CUA_BUILD_FIXTURE=1 go test -tags=integration ./internal/desktop -run '^TestNative(CuaFixtureBuild|MacWebKitFixtureBuild)$' -v
AICE_CUA_BUILD_FIXTURE=1 go test -tags=integration ./internal/app -run '^TestNativeMacPrintFixtureBuild$' -v
AICE_CUA_TEST_FIXTURE=1 go test -tags=integration ./internal/desktop -run '^TestNativeCuaFixtureLifecycle$' -v
```

The last command opens/closes only synthetic windows and checks the focus monitor.
Select one gate at a time using this command shape:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/desktop \
  -run '^TestNativeCuaMultiApp$' -count=1 -v
```

| Test selector | Required independent evidence |
| --- | --- |
| `TestNativeCuaMultiApp` | Three AppKit values/commits, nine captures, connection/session reuse, sentinel and shared-service survival |
| `TestNativeMacWebKitTransfer` | AppKit → local WebKit → AppKit, exact DOM/widget values and one commit per target; WebKit uses `type_text` on an empty field |
| `TestNativeMacInput` | Actual ASCII/Unicode insertion, single-key and hotkey selection, not merely a returned RPC |
| `TestNativeMacPixelClick` | Screenshot-derived click, resize refusal with zero commits, fresh-image recovery |
| `TestNativeMacWindowMove` | One-display negative-origin translation with one click/commit; not multi-monitor scaling |
| `TestNativeMacPointerButtons` | Exact double/right event counts and continuous sentinel focus |
| `TestNativeMacGestures` | Actual scroll offset and slider movement; background drag refusal is not success |
| `TestNativeMacCancel(Wait\|DispatchedClick)` | No queued click after cancellation; committed/pending click keeps unknown outcome and is not replayed |
| `TestNativeMacCursorLifecycle` | Own session/cursor absent → hidden → visible → removed, one commit and sentinel/service survival |

The pointer/gesture gates retain the [unresolved native findings](desktop.md#macos-input-limitations).
Focus restoration at the end cannot erase a temporary loss. Physical keyboard,
IME, desktop compositing and unrelated apps need separate checks.

Cursor appearance inspection additionally accepts
`AICE_CUA_NATIVE_CURSOR_HOLD_DIR=/absolute/fresh-empty-directory`. The fixture
writes `ready` with its App path; create `act` to allow one click, inspect after
`acted`, then create `finish` to release cleanup. Phases are bounded to four
minutes/test to nine. Markers prove synchronization, not visual correctness.
Inspect the Driver's transparent overlay, since target-only capture may omit it.
The stock session-label assertion is not currently accepted on 0.30.4.

Cold launch creates/registers/removes only its temporary AppKit bundle in
`~/Applications`; foreground drag can move the real pointer/activate its target.
They have separate opt-ins:

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_LAUNCH=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacLaunch$' -count=1 -v
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_FOREGROUND=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacForegroundDrag$' -count=1 -v
```

Actual CLI/TUI gates use scripted models with production Loop/Guard/managed MCP
and Session. Print checks source/image replay; Stop checks complete retained pairs
and cleanup. Mutation Stop requires independently observed commit while its result
is still pending, followed by unknown outcome and no replay:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app \
  -run '^TestNativeMac(DesktopPrint|WebKitPrint|DesktopStopTUI|DesktopStopMutationTUI)$' -count=1 -v
```

Settings repair can open OS permission UI and probe capture, so it is separately
authorized. It uses temporary settings but an existing installation/grants:

```sh
AICE_CUA_NATIVE_SETUP=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacDesktopSetupTUI$' -count=1 -v
```

It is not first installation/system-dialog acceptance. Offline
`TestDesktopActivityTUI` checks activity/folds/Stop with a synthetic backend.

### Native session idle expiry

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_EXPIRY=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacSessionExpiry$' -count=1 -timeout=9m -v
```

This waits for native default expiry, verifies old input does not mutate, then
recovers in a fresh Run with one commit. No TTL override, replay, private lifecycle
API or daemon restart is allowed. Keep the desktop unlocked throughout; session
disappearance alone is insufficient. This gate has no foreground sentinel.

### Native owned-proxy crash

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_PROXY_CRASH=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacProxyCrash$' -count=1 -v
```

After independent commit but before response, terminate only the owned proxy
handle. Require unknown outcome, no replay, explicit read-only recovery and the
same shared daemon. This does not validate interrupted gestures or OS revocation.

### Native same-run reconnection

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_RECONNECT=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacSameRunReconnect$' -count=1 -v
```

Discover metadata, retire only AICE's connection, and rediscover in the same Run
with a fresh native lifecycle. No capture, input, activation or model call occurs;
shared-daemon identity and occupancy must be preserved.

### Native discovery idle recovery

```sh
AICE_CUA_NATIVE=1 AICE_CUA_NATIVE_DISCOVERY_EXPIRY=1 go test -race -tags=integration ./internal/desktop -run '^TestNativeMacDiscoveryIdleRecovery$' -count=1 -timeout=9m -v
```

Wait six minutes without discovery while the fixture keeps only its explicit
session active. Sessionless discovery must remain usable or return recognized
session expiry; in the latter case, preserve the result, retire the connection
and explicitly rediscover in the same Run. Other errors fail. This tests the
implicit discovery lifecycle without capture/input/model calls; production sends
no keepalives.

### Explicit real-model desktop gate

First validate the scripted harness without provider access:

```sh
AICE_CUA_NATIVE=1 go test -race -tags=integration ./internal/app -run '^TestNativeMacManagedModelHarness$' -count=1 -v
```

The real-model gate uses a selected production provider, Loop, Guard, managed
catalog and Session for three synthetic AppKit/WebKit/AppKit forms. It accepts
only the managed route, checks exact fixture targets before dispatch and fails
on any scope refusal/approval. This test-only scope is not a product allowlist.
Independent values, exactly one commit each, delivered/replayed images, sentinel
and shared-service survival are all required; Loop completion alone is insufficient.

With provider/model and usage budget authorized, create a fresh empty output
directory and run:

```sh
AICE_CUA_NATIVE_MODEL=1 \
  AICE_CUA_MODEL_ROUTE='managed' \
  AICE_CUA_MODEL_REQUEST_BUDGET=80 \
  AICE_CUA_MODEL_PROVIDER='<configured-provider-id>' \
  AICE_CUA_MODEL_ID='<image-capable-model-id>' \
  AICE_CUA_MODEL_THINKING='<supported-thinking-level>' \
  AICE_CUA_MODEL_ARTIFACT_DIR='/absolute/fresh-empty-directory' \
  go test -race -tags=integration ./internal/app -run '^TestNativeMacActualModelDesktop$' -count=1 -v
```

This reads user credentials/configuration, not project settings/skills, and can
incur provider charges. Provider and thinking are required. Default limits are
20 requests, five minutes, 4,096 output tokens per response and 100,000 reported
tokens. Request budget accepts 1–200; the example allows discovery/readback room.
An independently authorized `AICE_CUA_MODEL_TOKEN_BUDGET` accepts 1–10,000,000;
limits are checked between operations and reported usage is not an exact billing
ceiling. Invalid/empty supplied values fail before credential reads.

Retain `task.txt`, `model-task.jsonl` and `report.json` outside the repository.
The report distinguishes `loop_completed` from `accepted`, and records effective
budgets, verification flags and timing without credentials/input bodies. Session
contains synthetic images/transcript. Model timing includes encoding/transport
and stream handling; tool timing includes Guard and recording;
`managed_call_ms` measures the native Run boundary. These overlapping intervals
are not additive, cold-start measurements or isolated network latency.

### Manual desktop checks

Use current source and the pinned Driver with three synthetic windows in distinct
apps: a text source, a local form with a submission counter, and an editor target.
Record AICE commit, OS/Driver/app versions and whether a human changed focus.
Do not use deleted `desktop_*` tools or a historical temporary binary.

1. In `/desktop`, keep Background only, run Setup/Repair, verify distinct
   permissions/capture results and save enable. Restart and confirm persistence.
   Existing grants verify repair, not first installation.
2. Ask AICE to load `computer-use`, discover `managed:cua`, copy a known Unicode
   string through the three windows and submit the form exactly once. Restrict
   the task to those windows; use no shell/file/browser substitutes. Require
   independent result readback and no repetition after uncertain input.
3. Keep the AICE terminal foreground and type with an IME without submitting.
   Check candidates, complete text, no misdirected input, visible agent cursor
   and no movement of the physical pointer by background actions. An accessibility
   mode required by an app is part of the recorded test conditions.
4. During a separate synthetic task, use Settings Stop/F6. Confirm no further
   actions, usable input afterward, retained effects and no automatic replay.
5. Test first installation/system grants only in a suitable clean environment.
   First cancel, then explicitly install/authorize the CuaDriver identity and
   verify actual capture before saving. Do not reset a working machine's TCC or
   delete its App to manufacture this condition.

Keep failed focus/cursor/postcondition observations even if switching windows
restores input. Operator reports do not replace independent assertions. Multiple
displays, OS revocation and interrupted gestures are separate acceptance cases.

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
