# Configuration

AICE resolves non-secret settings in this order, from highest to lowest
precedence:

1. `AICE_*` environment variables
2. `~/.aice/settings.json`
3. AICE defaults

Provider, model, and thinking are user-level choices shared by every
workspace. AICE does not read or write them in
`<workspace>/.aice/settings.json`. If a file remains there from an older
version, it is ignored.

For example, the global file can contain:

```json
{
  "model": "deepseek-v4-pro",
  "thinking": "high"
}
```

Supported settings are:

| Setting | Environment variable | Values |
| --- | --- | --- |
| Provider | `AICE_PROVIDER` | `deepseek`, `opencode-go` |
| Model | `AICE_MODEL` | provider models, e.g. `deepseek-v4-flash`, `kimi-k2.6` |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |

`thinking: "off"` disables thinking mode, so AICE does not persist a second
boolean that could conflict with the reasoning level.

## Interactive commands

The interactive TUI supports:

```text
/help
/settings
/login
/provider
/model
/thinking
/session
/tree
/checkout
/compact
/clear
/quit
```

`/help` lists these commands. `/login` opens a provider menu before hidden
credential input. `/provider`, `/model`, and `/thinking` open a value menu and
save the selected value directly to global settings. `/session` shows the
current Session file, `/tree` lists the Session branch tree and active leaf,
and `/checkout` chooses where the next Session branch starts. `/compact`
compacts the active branch at the current turn boundary. `/clear` clears the
visible transcript without changing Session history, and `/quit` exits AICE.
Menu-based commands do not accept typed arguments; choose values from their
menus instead. Successful setting changes update the current interactive
Session immediately.

## Credentials

Missing credentials do not prevent the interactive TUI from starting. Its
welcome screen suggests `/login`; submitting a normal prompt before login
returns the same guidance without closing AICE.

`/login` opens a provider menu and asks for that provider's API key using
hidden input. Press `Esc` or `Ctrl+C` to cancel the input and run `/login`
again at any time. The key is never included in slash-command arguments or the
visible transcript.

For non-interactive setup, the `config set-key` command reads the key from
standard input instead of accepting it as a command-line argument. Use
`--provider` to select the provider (`deepseek` is the default). For example,
persist an already exported key with:

```sh
printf '%s\n' "$AICE_OPENCODE_API_KEY" | go run ./cmd/aice config set-key --provider opencode-go
```

The command writes `~/.aice/auth.json` with mode `0600`. Provider credentials
are stored side by side, so logging in with one provider does not erase the
other. A process-level environment variable overrides the stored credential.

Supported credentials:

| Provider | Env variable | Auth file key |
| --- | --- | --- |
| DeepSeek | `AICE_DEEPSEEK_API_KEY` | `deepseek_api_key` |
| OpenCode Go | `AICE_OPENCODE_API_KEY` | `opencode_api_key` |

`AICE_DEEPSEEK_BASE_URL` and `AICE_OPENCODE_BASE_URL` remain environment-only
connection overrides.
