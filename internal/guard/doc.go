// Package guard provides built-in execution gates for AICE.
//
// Checks run inline before every tool execution, as an intrinsic agent
// capability rather than a plugin.
//
// Design principles:
//   - Policies are fixed: a secret-file rule, structural dangerous-command
//     detection over parsed shell words, and an ask for paths outside the
//     workspace. Only startup-discovered read-only roots are configurable.
//   - Single interception point: agent.Loop checks via the Guard interface
//     defined by the consumer (agent), never by the guard importing agent.
//   - Session grants cover file and directory paths, exact commands and
//     unknown tool names. MCP grants separately bind source, service,
//     connection, permission scope and tool schema. All are memory-only for
//     the current Session; new runs do not clear them, a new Session does.
package guard
