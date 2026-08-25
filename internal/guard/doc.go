// Package guard provides built-in execution gates for AICE.
//
// It mirrors the kernel of pi-guardrails but is not a plugin: checks run
// inline before every tool execution, as an intrinsic agent capability.
//
// Design principles:
//   - Policy matching is pure except for existence probes, which are injected
//     and used only when onlyIfExists is set.
//   - Files match with glob (with "/"-aware full-path vs basename) or optional
//     regex. Bash permission checks use structural AST matching; custom
//     patterns are substring or regex.
//   - Single interception point: agent.Loop checks via the Guard interface
//     defined by the consumer (agent), never by the guard importing agent.
//   - Session grants cover file and directory paths, exact commands, command
//     prefixes, and tool names. All are current-run and memory-only.
//     Persistent grants are a planned extension; see docs/architecture.md
//     Planned extensions and restraint.
package guard
