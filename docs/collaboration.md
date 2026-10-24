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

The CLI documentation checks in
[root_documentation_test.go](../internal/cli/root_documentation_test.go) normalize
CRLF to LF before matching headings and parsing tables. Their line-ending
regression covers both checkout formats on every host.

For macOS clipboard changes, use an isolated pasteboard:

```sh
go test -tags=integration ./internal/tui -run '^TestMacClipboardNativeFormats$'
```

Default tests use synthetic clipboard data and bounded helpers. Linux/Windows
clipboard and physical IME behavior require their native desktops. CLI-driven
TUI tests must wait for the expected reply and idle frame, not just an earlier
command's idle header; resize repaints can expose full asynchronous frames.
Match the current reply or cancellation and the READY header in the same frame.
A previous run's cancellation notice remains in the transcript while a later
run is still stopping. Failed input or repaint writes must report the pending
action and terminal transcript so a deadline is not reduced to a closed-pipe error.

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

Default tests use synthetic install and grant functions and perform no native
desktop calls. Computer Use runtime behavior is Cua's; check it through the
ordinary MCP path rather than AICE-specific native gates.

Installer artifact checks use already downloaded archives and do not download,
execute desktop actions or grant OS permissions:

```sh
AICE_CUA_TEST_ARCHIVE=/absolute/path/to/cua-driver-rs-0.38.0-darwin-universal.tar.gz \
  go test -tags=integration ./internal/deps -run '^TestNativeCuaArtifactExtraction$' -v
AICE_CUA_TEST_ARTIFACTS=/absolute/path/to/archives \
  go test -tags=integration ./internal/deps -run '^TestCuaNativeReleaseArchives$' -v
AICE_CUA_TEST_NATIVE_ARCHIVE=/absolute/path/to/native-archive \
  go test -tags=integration ./internal/deps -run '^TestNativeCuaPrivateInstallation$' -v
```

Manual check with authorization for OS prompts and desktop actions: run
`/desktop` → Set up / Repair, confirm `aice mcp status user:cua` shows an
approved connection, then `aice mcp connect user:cua` lists Cua's tools. A task
then exercises the ordinary connection prompt-free path, Guard tool prompts and
Cua's own results. Keep generated Sessions and captures outside the repository.

## Browser checks

Default tests use synthetic helper paths and perform no browser or helper
calls. Fake executable lookups must return host-absolute paths for installed
helpers, including Git Bash, so Windows discovery does not trigger unrelated
provisioning. Browser runtime behavior is agent-browser's; check it through the
ordinary MCP path: `/browser` → Set up / Repair, `aice mcp connect user:browser`
lists the core tools, then a task exercises Guard prompts, page actions and
screenshot images. Use a dedicated profile for CDP connections, never the
user's live profile.
