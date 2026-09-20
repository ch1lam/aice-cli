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
The build reuses the Go test toolchain and caches with module downloads disabled.

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

It uses an isolated session, a local data-URL form, snapshots, input actions,
screenshot PNG decoding, close/sidecar checks and a fresh generation. Never point
it at the user's live profile. Actual `/browser` TUI, external CDP connection and
Chrome Allow acceptance need a dedicated profile and a desktop. Keep native,
scripted-model and actual model evidence separate in the [acceptance matrix](browser.md#maintenance-and-verification).
