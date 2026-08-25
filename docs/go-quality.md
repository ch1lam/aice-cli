# Go Code Quality and Karpathy Guidelines

## Go Code Quality

- Read files in full before wide-ranging changes, before editing a file not already fully inspected, and whenever the user asks for an investigation or audit. Do not base broad conclusions on search snippets alone.
- Prefer clear, idiomatic Go over patterns copied from TypeScript. Keep functions focused, handle errors early, and avoid premature abstractions.
- Package names are short, lowercase, singular, and specific. Do not create `util` or `helper` packages. Existing `internal/jsonutil` and `internal/apitest` are approved narrow shared packages; do not add another general-purpose util package.
- Define small interfaces where they are consumed; accept interfaces and return concrete types.
- Use manual constructor injection. Avoid mutable package globals and side-effectful `init()` functions.
- Wrap errors with context using `%w`; use errors for expected failures and reserve panics for broken invariants.
- Use concrete types or generics instead of `any` when the data shape is known. Keep raw JSON at protocol/tool boundaries.
- Propagate timeouts and cancellation to all external calls. Bound retries and check the context between attempts.
- Comments explain intent, invariants, protocol quirks, and security decisions, not obvious syntax.
- Never remove or downgrade code or capability to work around type errors caused by outdated dependencies. Upgrade the dependency and adapt the code to its supported API.
- Do not preserve backward compatibility unless the user explicitly requests it. This includes the abandoned TypeScript implementation and its session/config formats.

## Karpathy Guidelines

Behavioral guidelines to reduce common LLM coding mistakes, adapted from the MIT-licensed `karpathy-guidelines` skill (derived from Andrej Karpathy's observations on LLM coding pitfalls). These bias toward caution over speed; use judgment for trivial tasks.

1. Think before coding: state assumptions explicitly, present alternative interpretations instead of picking silently, push back when a simpler approach exists, and stop to name anything that is unclear. Do not hide confusion.
2. Simplicity first: ship the minimum code that solves the problem. No speculative features, single-use abstractions, unrequested flexibility, or error handling for impossible scenarios. If 200 lines could be 50, rewrite.
3. Surgical changes: touch only what the request requires. Do not improve adjacent code, comments, or formatting; do not refactor what is not broken; match existing style. Clean up only orphans created by your own changes, and mention (do not delete) unrelated dead code.
4. Goal-driven execution: define verifiable success criteria and loop until verified ("write a test for invalid inputs, then make it pass"; "ensure tests pass before and after"). For multi-step tasks, state a brief plan with per-step verification.
