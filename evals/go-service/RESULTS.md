# Offline fixture validation

Date: 2026-09-05. Environment: `go version go1.26.5 darwin/arm64`, Python 3.14.
These measurements concern the evaluation assets, not an AICE/model run.

Command from the repository root:

```sh
GOCACHE=/tmp/aice-go-eval-cache python3 evals/go-service/verify.py
```

Observed output (exit 0):

```text
PASS extension: 4 tests passed [race, uncached]
PASS extension: expected failure: TestTags [race, uncached]
PASS bug: 4 tests passed [race, uncached]
PASS bug: expected failure: TestUnicodeBoundary [race, uncached]
PASS refactor: 5 tests passed [race, uncached]
PASS refactor: expected failure: TestLimit [race, uncached]
PASS reference: 6 tests passed [race, uncached]
Fixture validation passed; no model was evaluated.
```

The extension seed satisfies the baseline but rejects the newly required tag.
The bug seed satisfies ASCII/base and tag behavior but rejects a valid multibyte
note. The refactor seed satisfies all existing requirements but returns too many
matches for the new limit requirement. Negative controls are accepted only when
the specifically named test fails without a build failure; arbitrary nonzero
process exits do not count as successful defect detection. The final reference
passes all six HTTP test groups, including concurrent creation/listing/deletion
under the race detector and invalid requests that must not mutate state.

Additional command from `reference` (exit 0, no output):

```sh
GOCACHE=/tmp/aice-go-eval-cache GOPROXY=off GOTOOLCHAIN=local go vet ./...
```

The first attempt using the default Go cache encountered an OS permission error
opening a cache entry. No code change was needed to resolve it; the writable cache
above was used for the successful runs. HTTP tests require no socket permission.

Root integration initially exposed an asset isolation defect: storing the
acceptance source with a `.go` suffix made root `go test ./...` try to compile it
against AICE's module. The source now uses `.go.txt`, and the driver restores `.go`
only in a temporary candidate. After this correction, root
`GOCACHE=/tmp/aice-go-eval-cache go list ./...` exited 0 and listed 27 AICE packages,
with no `evals` packages. That command emitted a nonfatal module-cache permission
warning. Fixture validation was rerun after the correction with the output above.
Root package discovery is an isolation check, not evidence that AICE's full test
suite passes; the root suite must be verified separately.

## Reference maintenance review

This is an evidence-based review of the supplied reference, not a model score.

| Dimension | Assessment | Evidence |
| --- | --- | --- |
| Understanding | 2 | HTTP handlers decode and validate, call the concrete store, and respond; there is no hidden registration or service lookup. |
| State ownership | 2 | `store.go` owns the map, monotonically increasing ID, and mutex; each constructor creates one independent store. Returned notes contain only scalar values. |
| Change locality | 2 | Comparing the generated pre-limit behavior with the reference shows query parsing in the HTTP owner and filtering/sorting/truncation in the collection owner. No server bootstrap changes are needed. |
| Abstraction | 2 | There is one concrete collection owner; no interface, generic repository, or hypothetical persistence adapter is introduced. |
| Proportion | 2 | A standard ServeMux and three collection operations cover the requirements; the reference intentionally omits persistence and deployment concerns. |
| Verification | 2 | Acceptance imports only the handler factory and exercises HTTP status, JSON contents, order, mutation effects, Unicode boundaries, and concurrency. Seed negative controls demonstrate failures. |

Limitations: no real model was called, no token/cost or intervention measurements
exist, and these passing fixtures do not prove generated-code quality. The command
entry point is compiled by `go test ./...`; automatic tests exercise the HTTP
handler without launching a TCP listener. A future model evaluation must retain
its own intermediate snapshots and review evidence, especially the refactor-only
snapshot before the final feature, rather than reuse this reference review.
