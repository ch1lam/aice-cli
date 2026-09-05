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
The [release workflow](../.github/workflows/release.yml) owns release build and
packaging commands. Harbor has its own [integration guide](../integrations/harbor/README.md).

## Offline capability checks

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
The guides include review criteria and reference validation records.

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
