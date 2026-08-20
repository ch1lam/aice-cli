// Package guard provides built-in execution gates for AICE.
//
// It mirrors the kernel of pi-guardrails but is not a plugin: checks run
// inline before every tool execution, as an intrinsic agent capability.
//
// Design principles:
//   - Pure core: Rule.Check is a pure function (no I/O) except for existence
//     probes which are injected and used only when onlyIfExists is set.
//   - Two matching semantics: files use glob (with "/"-aware full-path vs
//     basename), commands use substring; both support regex opt-in.
//   - Single interception point: agent.Loop checks via the Guard interface
//     defined by the consumer (agent), never by the guard importing agent.
//   - Session grants are memory-only for now; persistent grants will reuse
//     config.Settings once GuardConfig is merged there.
package guard
