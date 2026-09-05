# Go notes service: offline engineering task series

This directory is an **evaluation sample**, not an AICE feature, helper service,
or additional production binary. Its isolated Go module uses only the standard
library; it does not alter AICE's module or dependencies. No provider, credential,
network download, or model invocation is involved in fixture validation.

The series exercises one product across creation, extension, defect repair, and
refactoring. Tests assert HTTP behavior through `notes.NewHandler() http.Handler`,
using `httptest` without opening a socket. Candidates may reorganize all private
implementation details. The factory is the single integration requirement.

The evaluator source is stored as `acceptance/service_test.go.txt` so root-level
Go package discovery does not treat it as an AICE package. The driver restores the
`.go` filename only inside its temporary candidate module. The reference has its
own `go.mod` and is likewise excluded from root `go test ./...`; run the fixture
command explicitly in addition to AICE's checks.

## Reproduce and run

From the AICE repository root, with Python 3 and Go 1.26.5 installed:

```sh
python3 evals/go-service/verify.py
python3 evals/go-service/prepare.py extension /tmp/notes-extension
python3 evals/go-service/verify.py --candidate /tmp/notes-extension --stage 1
```

`prepare.py` requires a destination that does not exist and writes only there.
It derives seeds from one reference tree using checked transformations, then
formats the generated Go code. Kinds: `greenfield`, `extension`, `bug`, `refactor`,
`reference`. Do not give the generator or reference solution to an evaluated model.
Give it the generated workspace and the applicable task specification below.

`verify.py --candidate PATH --stage N` copies the candidate into a temporary
directory, inserts evaluator-owned acceptance tests, and runs
`go test -race -count=1 -timeout=30s -json -run PATTERN ./...` with network module
access disabled. The original candidate is untouched. The command fails on build,
test, timeout, or race errors. This executes candidate code and is **not a security
sandbox**; evaluate untrusted submissions in an appropriate isolated environment.

If local cache permissions prevent compilation, choose a writable cache:

```sh
GOCACHE=/tmp/aice-go-eval-cache python3 evals/go-service/verify.py
```

Run the reference as a real local server if desired:

```sh
cd evals/go-service/reference
ADDR=127.0.0.1:8080 go run ./cmd/server
```

All storage is in memory and intentionally disappears on restart. Authentication,
disk storage, deployment, dependencies, and framework selection are out of scope.

## Task 1: build from zero

**Input:** `greenfield` seed (only `go.mod`). **Output:** runnable
`cmd/server`, package `notes` with `NewHandler`, and candidate-owned tests.

Build an in-memory HTTP notes service. Each handler owns independent state and is
safe under simultaneous HTTP requests. Support:

- `POST /notes` accepts a JSON object with `text`. Trim surrounding whitespace;
  accept 1–200 Unicode code points, including multibyte characters. Return 201 and
  a JSON object containing positive integer `id` and stored `text`.
- IDs strictly increase per handler and must not be reused after deletion.
- `GET /notes` returns 200 and a JSON array ordered by ascending ID, including `[]`
  for no notes. JSON responses have `application/json` content type.
- `DELETE /notes/{id}` returns 204 for an existing note and 404 for absent,
  invalid, or nonpositive IDs. Unknown routes return 404; unsupported methods on
  known routes return 405. Do not prescribe error response bodies.
- Reject malformed JSON, wrong field types, unknown fields, missing/blank/too-long
  text, trailing JSON values, and bodies exceeding 4096 bytes with 400. Rejections
  leave stored notes unchanged.
- `go run ./cmd/server` listens at `ADDR`, defaulting to `127.0.0.1:8080`.

**Acceptance:** `--stage 1`. Additional response fields are allowed. No persistence
or update endpoint is required.

## Task 2: add tags across the request/storage/query path

**Input:** the actual Task 1 result for a continuous model run; `extension` seed
for a reproducible isolated run. This seed passes all Task 1 acceptance.

Preserve Task 1 behavior. Add an optional `tag` to POST and returned note objects.
An omitted or empty tag is permitted. Nonempty tags contain only lowercase ASCII
letters and have at most 20 characters. Invalid tags return 400 without mutation.
Add `GET /notes?tag=work`: nonempty tags filter by exact equality; empty/omitted
filter lists all. Invalid filters return 400. Sorting still applies after filtering,
and no matches produce `[]`. Include candidate-owned tests for the change.

**Acceptance:** `--stage 2`. Review the diff for whether HTTP input, storage, and
query behavior are coherently owned, without requiring a particular file layout.

## Task 3: reproduce and repair a regression

**Input:** `bug` seed, which represents a Task 2 implementation with an injected
text-length regression. For longitudinal evaluation, the evaluator may inject
the equivalent behavior defect into the prior result while preserving its design.

Bug report: “A note containing 200 `界` characters is rejected even though the
documented limit is 200 characters. The same-length ASCII note succeeds.” Reproduce
the issue, fix its cause, and add a regression test. Preserve the 201-code-point
rejection and every other existing behavior. Do not change the requirement to bytes
or raise a numeric limit until examples happen to fit.

**Acceptance:** `--stage 3`. The self-check demonstrates that this seed passes base
and tag tests but fails the separately named Unicode boundary test.

## Task 4: refactor, then accept a new requirement

**Input:** `refactor` seed. It correctly implements Tasks 1–3 but mixes collection
mutation, locking, ID allocation, and HTTP routing inside one constructor.

First improve ownership and readability while preserving all observable behavior.
Keep the design proportionate to this small program. Explain who owns mutable
state, how callers reach it, and why any new abstraction is needed. Produce a
reviewable refactor-only diff/snapshot and run `--stage refactor` before receiving
or implementing the next requirement. Merely moving the same constructor to a
different file does not establish improved ownership.

**Subsequent requirement:** add `GET /notes?limit=N` with one integer value in
1–100. Missing limit means all results. Empty, duplicate, noninteger, out-of-range,
or overflowing values return 400. Apply limit after tag filtering and ID sorting;
never mutate the stored collection. Support combining `tag` and `limit`.

**Final acceptance:** `--stage 4`. Preserve the pre-feature snapshot so a reviewer
can distinguish behavior-preserving structure changes from the feature diff.

## Maintenance review and run record

Review each dimension as **0: unsupported**, **1: mixed**, **2: evidenced**, with
file/diff examples. Scores are discussion aids; they do not replace correctness.

| Dimension | Evidence to examine |
| --- | --- |
| Understanding | Can a reader trace POST → validation → state mutation → response without hidden registration or excessive jumping? |
| State ownership | Are mutation and synchronization rules in one comprehensible place? Are handlers independent? |
| Change locality | Does the post-refactor limit change avoid unrelated rewriting, duplicate policy, or coupling HTTP parsing to storage internals? |
| Abstraction | Does each interface/helper isolate an actual concept or consumer need rather than speculative future use? |
| Proportion | Are the implementation and failure paths as simple as the requirements allow? |
| Verification | Do tests exercise externally meaningful behavior, regressions, and rejected inputs rather than private structure? |

For future model runs record: AICE revision; model/provider and settings; task seed;
initial/final source snapshots; each acceptance command and exit code; elapsed time;
available token/cost measurements (otherwise `unavailable`); user interventions;
review scores with evidence; and unfinished requirements. Keep actual result
snapshots separate from reference solutions. Do not infer elegance from line count,
test success, or a model's completion message.

See [RESULTS.md](RESULTS.md) for the fixture's measured offline self-check. These
results validate the task assets and fault detection, **not model coding quality**.
