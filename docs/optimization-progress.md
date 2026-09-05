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
| 3a | Guard/app: Session-scoped grants reset at `/new`, exact command matching, deny before all asks, complete approval scope | Behavioral | Combined Guard and app/Loop regression tests, including yolo | Complete |
| 3b | App: restart-only Skills reminder and effective `/trust` choices | Behavioral | Startup temporary trust preserved; command behavior and documentation agree | Complete |
| 4a | Session: message entries, tree replay, safe branch boundaries, unknown interrupted results | Behavioral | New format round trips; old bytes untouched; no duplicate recovery/results/usage | Complete |
| 4b | App/Agent: persist each completed message before later side effects, unified terminal submission | Behavioral | Injected write/UI/provider/cancellation failures; print and TUI share semantics | Complete |
| 4c | Context: compact at paired model-round boundaries with frozen model configuration; stateless print uses memory | Behavioral | 200 rounds, at least three compactions, steering, repeated compaction and failure cases | Pending |
| 4d | Session consumers: navigation, display, usage, Harbor | Behavioral | Real CLI/TUI exercises; conversion fixtures and correct usage accounting | Complete for v3; automatic summary print totals remain in 4c |
| 5a | Existing prompt and Bash feedback: proportional engineering guidance, bounded head/tail output | Behavioral | Output/error regressions; custom prompt replacement unchanged | Complete |
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
Full tests and vet passed.

Guard now returns complete approval scopes only after checking every applicable
hard denial. The Loop requires every scope to be allowed and rejects invalid
results/replies. Real Guard + app + Loop tests use a counting fake tool to cover
multiple paths, dangerous-command scopes, denial order, yolo, once-grant isolation,
path deduplication, and cancellation. Full tests, vet, and race passed. The existing
engine-only `AllowSession` policy exemption remains unchanged; the app does not
expose or call it.

Session grants now reset when `/new` detaches, while invalid or active-run
commands leave them intact. Menu labels say "for this session". Guard and app
lifecycle regressions cover all grant kinds, reuse across runs and Loop rebuilds,
configured rules/read roots, and yolo preservation. Full tests, vet, and race
passed. Actual interactive verification is recorded below.

The Skills reminder now says restart only. `/trust` offers saved choices and
rejects temporary choice arguments; saved decisions explicitly take effect on
restart. Regressions verify both saved Trust and unchanged loaded context,
while startup temporary trust/ignore still changes prompt loading without
persisting. Full tests, vet, and race passed alongside the approval-scope fix.

On 2026-09-06, a binary built at `f16d590` was exercised through an actual PTY
with a temporary workspace and localhost-only OpenAI-compatible scripted server.
The server requested a read of a known temporary text file, then ended its
response; no paid model was used. The TUI showed the Session grant label,
performed a second read without asking, and asked again after `/new`. CLI
`session tree` still showed both prior interactions in the preserved file.
The `/trust` menu showed only three saved choices, `/skills` said restart,
and idle `/new` followed by exit left no Session file. Actual `--print` JSON
reported a paired error for the unapproved external read and created no Session
file. The first localhost request was blocked by the execution sandbox; the
permitted rerun passed. The temporary server was stopped after verification.
This covers the completed lifecycle fixes on macOS, not the pending v3 or
long-interaction behavior.

The Loop now offers one optional `MessageRecorder` boundary. Result owns terminal
assistant messages and tool results before callbacks can fail. Source inputs,
retries, actual/synthetic tool results, and final cleanup are recorded once;
the first recording error is sticky and prevents later effects. Regression
tests independently inject record/display/provider/cancellation failures, check
callback copies and counts, and compare recorded messages with returned truth.
Full tests, vet, and race passed at `6ca7811`; that prerequisite commit retained
v2 application storage. The following format migration activates the callback.

The v3 migration replaces `Turn` with one `MessageEntry` per ended source
message. Print and interactive execution now share that submission boundary;
interaction-end events and final Results no longer retry or duplicate saves.
Source prefixes can contain pending tool calls, while context, compaction and
checkout enforce complete pairing. Resuming after workspace verification adds
only missing unknown-outcome results, each durably; tree inspection does not
append them. Side snapshots publish only safe history, with validation outside
the short history lock. An incomplete live Session reports how to reopen or
start a new Session. No compatibility engine or second durable transcript was
added. Old versions, including malformed tails, are rejected without changing
bytes. Usage comes from source assistants and checkpoints once.

A 600-message storage exercise validates repeated call IDs and context safety
at every prefix. Removing repeated ancestor scans from snapshot validation
reduced the same local non-race observation from 15.924 to 8.968 seconds. This
is not a statistical performance benchmark or the pending 200-round Loop
acceptance. All Go tests, vet, and full race passed; the Session race test took
85.442 seconds in the integrated run. Final menu-wording corrections passed
focused app checks. Nine standard-library Harbor projection tests passed;
actual Harbor/Pydantic execution remains unverified. The converter preserves
physical source audit order and resolves tool results on their own parent chain.

Actual v3 CLI checks on 2026-09-06 used `/tmp/aice-v3-check` and a localhost-only
scripted model with dummy credentials. A temporary Bash tool appended one
character. Stateless print created no Session directory; explicit print wrote
four message records. A separate fixture copied the prefix ending at the tool
call after the effect had occurred. Tree inspection and rejected checkout left
it unchanged; two resumes added one unknown result and did not repeat the effect.
A v2 header with an incomplete tail retained the same SHA-256 after rejection.
Actual TUI resume displayed four message nodes, omitted the unpaired call from
checkout choices, checked out root, and detached with `/new` while preserving
the old file. The localhost server and TUI were stopped after verification.

Automatic compaction still runs at initial/follow-up boundaries in this commit.
A narrow explicit pending-input bridge prevents the newly durable input from
being summarized or appended twice. Step 4c will remove this bridge when Loop
compaction accepts the entire current context before each safe model request;
it also owns frozen summary configuration, stateless memory compaction and
complete print summary usage.

Summary model selection is now frozen independently of compaction timing.
Print shares its selected model service with summaries; interactive execution
constructs at most one summary service from that Run's captured configuration.
Manual TUI compaction uses the current `/model` selection, while standalone
CLI compaction resolves settings once after establishing there is work to do.
Tests change external settings during execution, check service construction
counts across repeated summaries, and exercise a TUI model change. Full tests,
vet, and race passed. Safe-boundary timing, memory compaction, model-aware
budget estimates, and summary retry/print accounting remain in step 4c.

Context estimates now reuse provider usage only for the requested provider/model;
unknown or changed identities fall back to full prompt/tool/message estimates.
Agent request protection and summary transcript sizing share their existing
reserve formula. A Session checkpoint invalidates only its old retained prefix,
so messages appended after it can establish fresh usage. A real Session reopen
regression checks both sides of that boundary. Full tests, vet, and race passed
(Session race: 86.247 seconds). Custom endpoint metadata assumptions are now
explicit in Configuration. Safe-round timing and memory compaction remain open.

Summary generation now accepts a successful final response after a provider
retry. Its checkpoint includes usage from every summary attempt once. Actual
503-to-success, terminal failure, and empty-final-response tests verify this
boundary and byte-preserving failure behavior. Full tests and vet, plus app
race tests, passed. Print summary accounting remains a separate step.

Bash now uses a dedicated fixed-capacity head/ring-tail collector. Grep retains
its original collector, and Loop control flow and process cleanup are unchanged.
Regression tests cover large/small writes, caller-buffer isolation, split UTF-8,
concurrent snapshots, exit 0/7, timeout, and cancellation. Full tests, vet, and
race passed. An actual CLI run with a localhost-only scripted model produced a
50,958-byte tool result containing the initial header, truncation marker, final
diagnostic, and exit code 7 in the next model request; stateless print created
no Session directory. The temporary server was stopped. This is feedback
verification, not a real-model coding-quality result.

Four short default-prompt additions make behavior/constraint tracing, local
state ownership, demonstrated abstraction needs, and evidence-based completion
explicit within the existing workflow. Custom project/global SYSTEM replacement
tests and default/Skills assembly tests passed. Full tests and vet passed on an
isolated snapshot of `582c16f` plus only this prompt change, so concurrent context
implementation was not part of that evidence. No fixed phase scheduler or
completion mechanism was added; real-model quality remains unmeasured.

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
