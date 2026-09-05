# Existing-capability optimization

This is the execution checklist for the architecture plan approved on
2026-09-05, starting at `f1dd281`. It tracks unfinished work and evidence;
the owning architecture, runtime, and user guides remain the contracts.
Remove this checklist after the completion report is consolidated into those
guides. Do not leave implementation markers in production code.

## Scope and working agreement

- Improve readability, state ownership, existing execution reliability, and
  engineering feedback. Keep one Go module and binary and the existing
  Agent/Provider/Tool/Guard/Session/UI boundaries.
- Do not add Goal, Plan, subagent, Memory, or other product features.
- Use message-level append-only JSONL with a new version. Reject old versions
  without modifying or deleting their files; no migration or compatibility
  reader is required.
- Use offline models and standard-library evaluation tasks. No paid model
  calls, new direct dependencies, pushes, releases, or PRs.
- The user explicitly requested layered local commits. Verify and inspect
  each single-intent change before committing it. Separate structural changes
  from behavior fixes and preserve unrelated work.

## Ordered change inventory

Each row can require several small commits; a later row must not hide unfinished
work in an earlier row. Temporary transitions must still build and pass tests.

| Step | Owner and change | Kind | Required evidence | State |
| --- | --- | --- | --- | --- |
| 1 | App/Agent/Session: characterize relevant persistence, cancellation, compaction, and permission boundaries | Tests | Existing invariants covered; known mismatches distinguished from intended behavior | Complete |
| 2 | App: separate environment, conversation, and active-run ownership; centralize history submission and configuration snapshots | Structural | Same user behavior, explicit lock/resource ownership, full tests and race | In progress |
| 3a | Guard/app: Session-scoped grants reset at `/new`, exact command matching, deny before all asks, complete approval scope | Behavioral | Combined Guard and app/Loop regression tests, including yolo | In progress |
| 3b | App: restart-only Skills reminder and effective `/trust` choices | Behavioral | Startup temporary trust preserved; command behavior and documentation agree | Pending |
| 4a | Session: message entries, tree replay, safe branch boundaries, unknown interrupted results | Behavioral | New format round trips; old bytes untouched; no duplicate recovery/results/usage | Pending |
| 4b | App/Agent: persist each completed message before later side effects, unified terminal submission | Behavioral | Injected write/UI/provider/cancellation failures; print and TUI share semantics | Pending |
| 4c | Context: compact at paired model-round boundaries with frozen model configuration; stateless print uses memory | Behavioral | 200 rounds, at least three compactions, steering, repeated compaction and failure cases | Pending |
| 4d | Session consumers: navigation, display, usage, Harbor | Behavioral | Real CLI/TUI exercises; conversion fixtures and correct usage accounting | Pending |
| 5a | Existing prompt and Bash feedback: proportional engineering guidance, bounded head/tail output | Behavioral | Output/error regressions; custom prompt replacement unchanged | Pending |
| 5b | Offline evaluation: Go HTTP service and Python data CLI lifecycles | Evaluation | Requirements, independent tests, reference implementations, review rubric, recorded runs | Complete |
| 5c | Documentation and completion audit | Documentation | Requirement-by-requirement evidence and honest limitations | Pending |

## Baseline and gaps

The planning review ran `go test ./...`, `go vet ./...`, and
`go test -race ./...` successfully on macOS at `f1dd281`. The race suite needs
localhost listeners for `httptest`; the first sandboxed attempt was denied
that facility, and the permitted rerun passed. These results do not establish
Linux or Windows execution, or real-model coding quality.

Existing tests already cover natural stop, steering and follow-up ordering,
invalid streamed calls, graceful cancellation after mutation, provider failure
after successful tools, append-only branches, corrupt JSONL rejection, and
compaction at complete-interaction boundaries. The latter boundary is current
behavior, not the new design: it must change without weakening tool pairing.

Known Guard and startup mismatches are documented in
[Maintenance](maintenance.md#known-discrepancies). Resolve entries there as
their fixes land; do not reinterpret mismatches as accepted behavior.

Step 1 adds `internal/app/persistence_boundary_test.go`: actual successful tool
results survive display failure, final display failure does not duplicate saved
messages, and persistence failure prevents queued follow-up and publication of
unsaved history. Assertions use restored context rather than physical turn
counts. Focused normal/race tests, `go test ./...`, and `go vet ./...` passed.

The first structural step moves transcript state and its begin/register/commit/
end/reload/snapshot operations into a named `conversationState`. The application
still coordinates settings, lazy creation, commands, and compaction. Lock order
and version 2 persistence behavior are unchanged. Full tests, vet, and race
passed; this is groundwork, not completion of the message-level redesign.

Exact command grants now use raw whole-string equality independently of
configured patterns. `TestGuardExactCommandGrant` and
`TestGuardExactCommandGrantPreservesOtherChecks` cover changed arguments,
compound commands, whitespace/quoting, configured patterns, and denial precedence.
Full tests and vet passed. Aggregate rule evaluation remains outstanding.

Session grants now reset when `/new` detaches, while invalid or active-run
commands leave them intact. Menu labels say "for this session". Guard and app
lifecycle regressions cover all grant kinds, reuse across runs and Loop rebuilds,
configured rules/read roots, and yolo preservation. Full tests, vet, and race
passed. Actual interactive verification remains part of the completion audit.

The Go HTTP evaluation family is in `evals/go-service`. It includes independent
HTTP acceptance, generated starting points, one reference implementation, and
an evidence-based maintenance rubric. An independent rerun of `verify.py`
passed all reference tests under the race detector and detected each intended
seed failure. Reference vet passed. The sample module is isolated from AICE's
runtime module; these are fixture checks, not a model-quality result.

The Python CSV evaluation family is in `evals/python-cli`. An independent
`fixture.py self-check` rerun passed 21/25/29/31 cases across the four stages;
each generated fault was detected at its expected case, including a CRLF output
mutation checked as bytes. Its validation record includes a maintenance review
and limitations. Both families cover creation, extension, defect repair, and
behavior-preserving refactoring followed by a new requirement; actual model
runs and their code-quality measurements remain unperformed.

## Completion audit

- [ ] A request can be traced through creation, mutation, cancellation,
  persistence, and close, with a named state owner at each step.
- [ ] Session authorization survives runs/model changes but resets at `/new`;
  exact grants and all hard-deny/ask combinations have regression coverage.
- [ ] Messages are durable before dependent tool effects; recovery marks unknown
  outcomes, never replays old calls, and is idempotent.
- [ ] Old Session files remain byte-for-byte intact when rejected.
- [ ] Branching and repeated compaction preserve source history and tool pairs.
- [ ] One request completes 200 simulated model rounds and at least three
  compactions while preserving an injected user correction.
- [ ] Interruptions cover assistant persistence, tool execution, tool-result
  persistence, summary generation, and checkpoint persistence.
- [ ] Cancellation, provider errors, output errors, persistence errors, malformed
  streams/calls, oversized outputs, and insufficient context have evidence.
- [ ] Stateless print creates no Session file; print/TUI/recovery/new/side
  conversation paths retain their intended behavior.
- [ ] Usage and Harbor conversion count messages and summary usage exactly once.
- [ ] Both offline task families cover greenfield, cross-module change,
  reproducible bug fixing, and refactoring followed by another change.
- [ ] Reference solutions pass independent acceptance tests; maintainability
  is reviewed explicitly rather than inferred from simulated model success.
- [ ] Full test/vet/race and actual affected CLI/TUI flows are verified, and
  platform/model limitations are reported.
- [ ] Owning documents match implementation; local commits are single-intent;
  no task-owned uncommitted changes remain and nothing was pushed.
