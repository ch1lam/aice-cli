# Agent Client Protocol

`aice acp` serves the [Agent Client Protocol](https://agentclientprotocol.com)
over stdio, so editors and evaluation harnesses drive AICE without the TUI;
[Evaluation](evaluation.md) runs it in Harbor.
It is another consumer of the `interaction` contracts: the Agent Loop, Guard,
Sessions and settings behave exactly as in interactive use.

```sh
aice acp --workspace . [--session path.jsonl] [--approve | --no-approve] [--yolo]
```

Stdout carries only protocol messages; diagnostics go to stderr. Project Trust
is never prompted over stdio: without `--approve`, an undecided project's
local resources are ignored. Credentials, provider and model come from AICE
settings and environment variables (`AICE_PROVIDER`, `AICE_MODEL`,
`AICE_THINKING`, provider API key variables); see [Configuration](configuration.md).

## Sessions

One process serves one current AICE Session. `session/new` makes a fresh
durable Session current and returns its Session ID; a `--session` file without
messages is used as that Session, otherwise the previous one is detached as
`/new` does. `session/load` and `session/resume` open a Session of the served
project by ID, recovering interrupted tools like `/history`; `load` replays its
active branch as user, thought, message and tool-call updates. Switching is
refused while a prompt runs. `cwd` must name the served workspace.
Client-provided `mcpServers` are rejected: MCP services come from AICE
configuration and its Guard permissions ([MCP](mcp.md)).

## Prompts and updates

| ACP | AICE |
| --- | --- |
| prompt text, `resource_link`, `image` | `RunInput` prompt text, explicit file references for `file://` links (other links stay text), images |
| `session/cancel` | Cancels the running Agent run; stop reason `cancelled` |
| `agent_message_chunk` / `agent_thought_chunk` | Streamed assistant text and thinking deltas |
| `tool_call` | Tool start: `kind` and `locations` from the Guard's action extraction, `rawInput` the call's actual arguments |
| `tool_call_update` | Tool end: `completed` or `failed`, `kind` and `locations` from the actions recorded on the result, output text, unified diff text for edits |
| `usage_update` | After each assistant message: context occupancy and the Session's cumulative cost |
| `session_info_update` | `title` of the current Session once its generated title is saved, after the first completed prompt of an untitled Session ([Sessions](execution-sessions.md#sessions)); sent asynchronously, possibly after the prompt response |
| `session/request_permission` | A Guard ask, with the Guard's options; `allow-once` maps to `allow_once`, session and saved grants to `allow_always`, the deny option to `reject_once` |
| `tool_call_update` during a permission request | The ask's advisory [summary](execution-sessions.md#permission-prompt-summaries) as text content (`Why:`, `Does:`, `Risk:` lines), sent only while the request is open; the tool end's output replaces it |
| `session/set_mode` | Permission mode `default` or `auto` for the next tool call, not saved |
| config option `model` | The effective `provider/model`; setting it accepts only that value |

The tool vocabulary is shared with print JSON events
([contracts](contracts.md#print-ndjson-events)). Stop reasons: `max_tokens` and
`refusal` follow the last assistant message, a Loop run limit (turns, budgets,
no progress, Guard denials) is `max_turn_requests`, otherwise `end_turn`. Other
run failures are JSON-RPC errors. The prompt response `usage` is that prompt's
usage in ACP's inclusive convention: input includes cache reads and writes.

A Guard deny never becomes a permission request, so no client answer can allow
it. A cancelled, failed or empty permission answer selects the Guard's deny
option. The unattended `allow` mode of a client therefore lifts asks only, as
`--yolo` does.

## Deliberate limits

- Model selection belongs to AICE settings. ACP declares the effective model
  so clients can confirm and record it, but cannot switch it.
- Message IDs (`messageId`) are unstable in ACP and must be UUIDs; AICE does not
  send them.
- ACP `diff` content requires complete old and new file text, which AICE results
  do not retain; edits report the tool's unified diff as text.
- A tool call is reported `in_progress` when the Loop dispatches it, before the
  Guard decides; the Loop has no separate execution-start event. A permission
  request during that call marks the wait for approval instead of `pending`.
- A permission request cannot change once sent, so its summary arrives as a
  separate update. A tool whose end reports no output or diff keeps the
  summary as its content.
- `request_user_input` is not offered: ACP has no question request, so the
  model states missing information in its reply.
- Branches, `/btw`, Settings and slash commands have no ACP mapping.

## Verification

`TestACPServesSessionsThroughInteractionContracts` drives `aice acp` with the
SDK's client over pipes, including a second process that loads the Session.
Its messages can be checked against the ACP schema pinned by `go.mod`:

```sh
ACP_TRANSCRIPT=/tmp/acp.jsonl go test ./internal/app -run TestACPServes -count=1
python3 evals/acp/validate_schema.py /tmp/acp.jsonl   # requires jsonschema
```

The validator selects each message's definition by method, matching responses
to their requests, and uses the SDK's unstable schema because AICE sends usage
updates.
