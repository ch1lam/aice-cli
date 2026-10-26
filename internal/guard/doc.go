// Package guard provides built-in execution gates for AICE.
//
// Checks run inline before every tool execution, as an intrinsic agent
// capability rather than a plugin.
//
// Design principles:
//   - Built-in policies are fixed. Inside the workspace, ordinary reads,
//     writes and commands allow; outside it, reads and writes ask separately.
//     Bash is parsed into commands whose consequences (privilege, disk,
//     fetch-exec, permissions, delete, lost work, outbound) ask. Secret files
//     and credential stores deny; AICE control files ask on every write.
//   - Explicit settings rules order deny over ask over allow. Allow rules
//     cover outside access and command findings, never the denials or
//     control files above.
//   - Single interception point: agent.Loop checks via the Guard interface
//     defined by the consumer (agent), never by the guard importing agent.
//   - Session grants cover file and directory paths per operation, exact
//     commands and unknown tool names. MCP grants separately bind source,
//     service, connection, permission scope and tool schema. All are
//     memory-only for the current Session; new runs do not clear them, a new
//     Session does.
package guard
