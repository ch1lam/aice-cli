# Go Engineering

## Working method

- Define the behavior to preserve or change before coding. Identify the
  responsible package and the smallest useful verification.
- Read affected files in full, including relevant callers and tests. For an
  audit, state which paths were inspected and which remain unverified.
- Fix the cause with a focused change. Avoid speculative features, single-use
  frameworks, unrelated cleanup, and abstractions without a concrete consumer.
- Separate behavior changes, structural refactors, and imported upstream code
  into independently reviewable steps. Git and verification rules live in
  [Collaboration](collaboration.md).
- When evidence conflicts, use [Maintenance](maintenance.md#resolving-discrepancies).
  Do not turn uncertainty into either an invented design rule or a silent fix.

## Implementation defaults

- Use the toolchain declared in [go.mod](../go.mod) and the pinned dependencies.
  Prefer idiomatic Go and the standard library. New dependencies follow
  [Architecture](architecture.md#dependency-and-ownership-rules).
- Keep functions focused, handle errors early, and name packages after their
  responsibility. Avoid vague `core`, `services`, `utils`, or `helpers`
  packages. Existing narrow shared packages are listed in the
  [package map](architecture.md#package-map).
- Define small interfaces at the consumer boundary when substitution or testing
  needs them. Accept interfaces and normally return concrete types. Wire
  dependencies through constructors; do not hide them in globals or `init()`.
- Prefer useful zero values. Initialize maps before writing; use nil versus
  empty slices according to the API/JSON contract. Clone mutable data when
  crossing ownership boundaries; do not add blanket copying everywhere.
- Use concrete types for known shapes. Use generics when a shared algorithm
  needs them; keep `any` and raw JSON at genuinely open protocol/tool boundaries.
- Return expected failures as errors. Add actionable operation context with
  `%w`, inspect chains with `errors.Is`/`errors.As`, and report errors once at
  the appropriate boundary. Do not discard write, flush, or close failures
  where durability matters. Reserve panics for broken internal invariants.
- Pass context explicitly and bound external work. Every goroutine needs an
  owner, cancellation, and an exit/wait path. Runtime ownership and the bounded
  persistence-cleanup exception are defined in
  [Contracts](contracts.md#concurrency-and-tui).
- Validate untrusted input before side effects. Preserve tool result pairing,
  resource bounds, and Guard checks. Host tools and project prompt loading have
  different access boundaries; see [Execution](execution-sessions.md) and
  [Project Trust](project-trust.md).
- Comments explain ownership, intent, invariants, and protocol quirks. Avoid
  implementation-plan labels and claims about future wiring. When behavior
  changes, inspect nearby comments as well as Markdown documentation.

## Tests and compatibility

Use standard `testing`, local fakes, `httptest`, and temporary directories when
sufficient. Test observable behavior, boundary failures, and regressions;
avoid assertions that only mirror private implementation structure. Use named
subtests for distinct cases. Parallelize tests only when they do not share
mutable state or process environment. Default tests must not need provider
credentials or paid APIs. Required commands live in
[Collaboration](collaboration.md#verification-commands).

The removed TypeScript runtime and its formats are not compatibility targets.
Internal Go APIs may change with their callers; do not retain obsolete shims
without a current need. This does not waive the current
[NDJSON contract](contracts.md#print-ndjson-events) or permit silently discarding
Session history. For a format or user-facing breaking change, identify affected
readers/integrations and explicitly decide versioning, migration, or rejection
behavior before implementation. Pre-stable formats may evolve; that is not a
blanket instruction to break them during unrelated work.

When a dependency API mismatch blocks work, first verify the pinned API and
local usage. Do not remove a capability to make the build pass, and do not
blindly upgrade dependencies. Explain and verify any necessary upgrade within
its own scope.

External skill examples are supplementary. Repository-specific ownership,
dependency, compatibility, and git rules take precedence over generic patterns
such as global Cobra registration, mandatory helper libraries, or automatic
commits.
