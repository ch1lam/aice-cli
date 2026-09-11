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

The [CI workflow](../.github/workflows/ci.yml) runs race tests and vet on
Linux, macOS, and Windows. A local pass proves only the tested platform; report
unavailable tooling or platform checks rather than claiming they passed.
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

## Installer checks

Installer tests use local release fixtures and simulated network failures; they
do not download releases or change the user's PATH:

- macOS/Linux: `sh -n scripts/install.sh` and `python3 scripts/test_install.py`.
- Windows: `powershell -NoProfile -File scripts/test_install.ps1` and
  `pwsh -NoProfile -File scripts/test_install.ps1`.

The CI matrix runs these on their native platforms, including Windows
PowerShell 5.1 and PowerShell 7. They cover release pinning, checksum rejection,
copy/replacement failures, cleanup, and path handling.

## Offline capability checks

Credential-lock retry tests inject filesystem errors and use a virtual clock
to cover Windows access denial, contention, cancellation, and timeout on every
platform. They do not require an open directory handle to prevent recreation;
that behavior varies across Windows filesystems and versions. Real temporary
directory tests still cover lock ownership and concurrent token refresh with
a local fake OAuth server, without accessing user credentials or remote APIs.

The default Go suite includes [long-task acceptance](../internal/app/long_task_test.go):
one interactive input with an in-run correction, and one stateless print input,
each complete 200 scripted main model requests and at least three real application
compactions. Tools perform local reads; summary generation uses a scripted model.
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

## Git and Collaboration

- Multiple sessions may share this worktree. Preserve unrelated staged, unstaged, and untracked changes.
- Modify and stage only explicit files owned by the current task. Never use `git add .`, `git add -A`, `git stash`, `git reset --hard`, `git checkout .`, or force push.
- Never commit unless the user asks. Before committing, inspect `git status` and the exact staged diff.
- When the user asks for incremental commits, make one independently verified commit after each completed small step.
- When asked to commit, follow the repository's existing short gitmoji/conventional subject style and keep each commit to one intent. Do not use Pi package scopes or release conventions.
- If a conflict touches a file not modified for the current task, stop and ask the user instead of resolving it speculatively.

## Browser checks

The default browser/dependency tests use fake commands and local HTTP fixtures,
not downloads or user profiles. The opt-in native test requires the verified
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
