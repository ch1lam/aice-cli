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
| Provider | `AICE_PROVIDER` | `deepseek` |
| Model | `AICE_MODEL` | `deepseek-v4-flash`, `deepseek-v4-pro` |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |

`thinking: "off"` disables thinking mode, so AICE does not persist a second
boolean that could conflict with the reasoning level.

## Interactive commands

The interactive TUI supports:

```text
/settings
/login
/provider
/model
/thinking
/checkout
```

`/login` opens a provider menu before hidden credential input. `/provider`,
`/model`, and `/thinking` open a value menu and save the selected value
directly to global settings. `/checkout` lists the Session root and current
tree entries. These interactive commands do not accept typed arguments; choose
values from their menus instead. Successful setting changes update the current
interactive Session immediately.

## Credentials

Missing credentials do not prevent the interactive TUI from starting. Its
welcome screen suggests `/login`; submitting a normal prompt before login
returns the same guidance without closing AICE.

`/login` asks for the DeepSeek API key using hidden input. Press `Esc` or
`Ctrl+C` to cancel the input and run `/login` again at any time. The key is
never included in slash-command arguments or the visible transcript.

For non-interactive setup, the `config set-key` command reads the key from
standard input instead of accepting it as a command-line argument. For
example, persist an already exported key with:

```sh
printf '%s\n' "$AICE_DEEPSEEK_API_KEY" | go run ./cmd/aice config set-key
```

The command writes `~/.aice/auth.json` with mode `0600`. A process-level
`AICE_DEEPSEEK_API_KEY` overrides the stored credential.

`AICE_DEEPSEEK_BASE_URL` remains an environment-only connection override.
