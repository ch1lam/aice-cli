# Tool Execution and Sessions

## Tool Execution and Sandbox Boundaries

- AICE does not have an intrinsic permission or approval system. By default, built-in tools run with the filesystem, process, network, and credential permissions of the AICE process, matching Pi's ownership model.
- Do not add approval prompts or authorization policy to the Agent Loop. Isolation and policy belong to an explicitly selected container, sandbox, execution environment, or extension.
- Keep correctness and resource-safety checks even when no sandbox is active: validate arguments, reject malformed paths and inputs, propagate cancellation, bound time and output, preserve exit status, and pair every tool call with a result.
- Path handling must be deterministic and must honor the selected execution environment. A workspace helper is not a security sandbox and must not be described as one.
- Structured command execution should use `exec.CommandContext` with executable and arguments separated. The `grep` tool invokes `rg` this way and must place `--` before the model-supplied pattern and path. A Pi-compatible `bash` tool may intentionally invoke a shell; make that boundary explicit and apply the same cancellation, process-tree, environment, and output controls.
- The execution environment owns working-directory, environment-inheritance, filesystem, network, and executable policy. Do not hard-code those policies into the Agent Loop.
- Bound time, stdout, stderr, and total captured output. Preserve exit status and distinguish a command failure from an internal tool failure.
- Ensure cancellation terminates the complete spawned process tree and does not leave background processes behind.
- Redact secrets, credentials, and sensitive prompt content from logs and persisted diagnostic data.

## Sessions and Compaction

- Persist the complete original session as append-only JSONL. Do not overwrite source history during compaction.
- Persist complete turns and compaction checkpoints as tree nodes with stable IDs and parent IDs. Persist backtracking as append-only leaf moves; never delete the abandoned branch.
- Derive the active transcript from the root-to-leaf path. After moving the leaf to an older node, the next complete turn becomes a new child and therefore creates a branch.
- Keep session storage, the context sent to the model, and the TUI viewport as separate representations.
- Run compaction only at a complete turn boundary. Never split an assistant tool call from its tool result or mutate history concurrently with an active run.
- Compaction applies only to the active branch. Its checkpoint must retain a stable first-kept turn ID so recovery and later branching do not depend on global array indexes.
- A compacted context is a summary plus recent messages; it is a derived view, not a destructive rewrite.
- Start with usage accounting, context-limit protection, and manual compaction. Add automatic compaction only after the JSONL and recovery behavior are stable.
