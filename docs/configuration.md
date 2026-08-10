# Configuration

## Settings and precedence

Non-secret settings resolve from highest to lowest priority:

1. `AICE_*` environment variables.
2. Global `~/.aice/settings.json`.
3. AICE defaults.

AICE ignores project `.aice/settings.json`. Provider, model, reasoning, and
credentials are user-level choices shared by every workspace.

Example global settings:

```json
{
  "provider": "opencode-go",
  "model": "kimi-k2.6",
  "thinking": "high",
  "default_project_trust": "ask"
}
```

| Setting | Environment variable | Supported values |
| --- | --- | --- |
| Provider | `AICE_PROVIDER` | `deepseek`, `opencode-go`, `openai` |
| Model | `AICE_MODEL` | A model in the selected provider's catalog |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |
| Default Project Trust | none | `ask`, `always`, `never` |

The default thinking level is `medium`. AICE aligns it to the nearest level
supported by the selected model, preferring the next higher level. The
requested level remains saved so switching models can restore it. `/settings`
shows the effective level; `/thinking` lists only valid choices for the active
model. Models without thinking support ignore the setting.

The built-in OpenAI catalog intentionally stays small: `gpt-5.6`,
`gpt-5.6-terra`, and `gpt-5.6-luna`. `gpt-5.6-terra` is the default because it
balances model capability and cost. These models use the official Responses
API and support `off`, `low`, `medium`, `high`, `xhigh`, and `max` reasoning
levels.

`default_project_trust` defaults to `ask`. Automation should use `--approve`
or `--no-approve` rather than a broad environment override. See [Project Trust
and prompts](project-trust.md) for protected files and decision order.

## Credentials and connection overrides

Credentials are stored by provider in `~/.aice/auth.json` with file mode
`0600`. A process environment variable overrides the stored key.

| Provider | API key environment variable | Auth file key | Base URL override |
| --- | --- | --- | --- |
| DeepSeek | `AICE_DEEPSEEK_API_KEY` | `deepseek_api_key` | `AICE_DEEPSEEK_BASE_URL` |
| OpenCode Go | `AICE_OPENCODE_API_KEY` | `opencode_api_key` | `AICE_OPENCODE_BASE_URL` |
| OpenAI | `OPENAI_API_KEY` | `openai_api_key` | `AICE_OPENAI_BASE_URL` |

In the TUI, `/login` opens a provider menu and hidden input. Missing
credentials do not prevent the TUI from starting, but a normal prompt asks the
user to log in first.

For non-interactive setup, send the key on standard input so it does not appear
in command-line arguments:

```sh
printf '%s\n' "$OPENAI_API_KEY" | \
  aice config set-key --provider openai
```

Provider keys are stored side by side; updating one does not erase another.

## Command-line options

```text
aice [--print <prompt>] [flags]

--workspace <path>   working directory for Agent tools (default .)
--session <path>     Session JSONL file to create or resume
--print, -p          print one response and exit
--approve, -a        trust project-local resources for this run
--no-approve         ignore project-local resources for this run
--version, -v        show the version
```

`--print` requires exactly one prompt argument. Without `--session` it does
not persist the run. Session navigation and compaction commands are documented
in [Tool execution and Sessions](execution-sessions.md#sessions).

## Interactive commands

| Command | Effect |
| --- | --- |
| `/help` | List commands |
| `/init` | Create or improve root `AGENTS.md`; loaded after restart |
| `/settings` | Show effective model, Trust state, and configuration paths |
| `/login` | Select a provider and store its key through hidden input |
| `/provider` | Select and save the global provider |
| `/model` | Select and save a model from that provider |
| `/thinking` | Select and save a supported reasoning level |
| `/trust` | Save or apply a Trust choice; prompt loading changes after restart |
| `/session` | Show the Session ID, path, active leaf, and counts |
| `/tree` | Show all Session branches |
| `/checkout` | Select where the next branch starts |
| `/compact` | Append a compaction checkpoint for the active branch |
| `/clear` | Clear the viewport without changing Session history |
| `/quit` | Exit AICE |

Menu commands do not accept typed values; select an option from the menu.
Provider, model, and thinking changes apply to the current Session immediately
and are also saved globally. Press `?` for keyboard shortcuts.

## Interactive input delivery

The composer remains active while an Agent run is working:

| Input | Effect while an Agent run is active |
| --- | --- |
| `Enter` | Send a steer into the active run at its next safe boundary |
| `Ctrl+Enter` | Queue a separate prompt to run after the active run |
| `Shift+Enter`, `Alt+Enter`, or `Ctrl+J` | Insert a newline |
| `Ctrl+C` | Cancel the active response |

A waiting steer appears immediately in the transcript as a user message with
a distinct color and animated dashed rail. Queued prompts stay above the draft
inside the composer as indented `↳` previews, separated from the draft by a
blank line. Each multi-line queued prompt shows its first line followed by
`...`; multiple prompts remain in submission order. If a run finishes before
it accepts a pending steer, AICE promotes that steer to the queue instead of
dropping it.

## Operational environment variables

| Variable | Effect |
| --- | --- |
| `AICE_NO_DEP_INSTALL=1` | Do not download missing ripgrep or Windows Git Bash helpers |
| `AICE_NO_UPDATE_CHECK=1` | Disable the daily interactive update check |

Installation, helper provisioning, and self-update details live in
[Installation and updates](installation.md).
