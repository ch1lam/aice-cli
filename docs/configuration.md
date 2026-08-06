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
  "thinking": "high",
  "default_project_trust": "ask"
}
```

Supported settings are:

| Setting | Environment variable | Values |
| --- | --- | --- |
| Provider | `AICE_PROVIDER` | `deepseek`, `opencode-go` |
| Model | `AICE_MODEL` | provider models, e.g. `deepseek-v4-flash`, `kimi-k2.6` |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |
| Default project trust | — | `ask`, `always`, `never` (default `ask`) |

`thinking: "off"` disables thinking mode, so AICE does not persist a second
boolean that could conflict with the reasoning level.

`default_project_trust` is global-only and has no environment variable:
automated runs use the `--approve` / `--no-approve` flags explicitly instead.
`always` trusts every project's local resources, `never` ignores them, and
`ask` (the default) prompts interactively only when a project has protected
resources.

## Project Trust

Project Trust gates the loading of project-local startup resources before AICE
reads them. It is an input-loading guard, not a sandbox: untrusted projects
still run with full host permissions, and tools are never wrapped or approved
per call.

Protected resources (workspace root only):

- `AGENTS.md` — appended to the system prompt when trusted
- `.aice/SYSTEM.md` — replaces the base system prompt when trusted
- `.aice/APPEND_SYSTEM.md` — appended to the system prompt when trusted

Decisions are resolved once per startup in this order:

1. `--approve` / `--no-approve` for this run
2. A saved decision in `~/.aice/trust.json` (nearest ancestor directory)
3. The global `default_project_trust` policy
4. An interactive prompt when the policy is `ask`, resources exist, and a UI
   is available; non-interactive runs fail closed and ignore project resources

The trust store is global and versioned:

```json
{
  "version": 1,
  "projects": {
    "/canonical/work": true,
    "/canonical/work/untrusted-repo": false
  }
}
```

Keys are canonical paths (symlinks resolved). The interactive startup prompt
offers `Trust`, `Trust parent folder`, session-only choices, and denial. The
`/trust` slash command saves the same decisions for future runs; `/settings`
shows the effective decision, its source, and the trust store path. `.aice/sessions`
never triggers Trust, and no decision is ever written into a Session.

## System Prompt

AICE assembles the agent system prompt per run:

- The base comes from the project `.aice/SYSTEM.md` when trusted, then the
  global `~/.aice/SYSTEM.md`, then the built-in default.
- The project `AGENTS.md` at the workspace root is appended to the base when
  trusted.
- `APPEND_SYSTEM.md` is appended last using the same precedence and is labeled
  with its source path.
- Project prompt files are only read through `os.Root` confinement, must be
  regular UTF-8 files under 64 KiB, and never replace the fixed compaction
  prompt.

The `/init` command creates or improves `AGENTS.md` in the workspace root using
the current model. When the file did not exist before, `/init` also records a
trusted decision for the workspace so the freshly generated file does not
trigger a trust prompt on the next run. The generated file, like any trust
decision, is loaded on restart, not hot-reloaded.

## Interactive commands

The interactive TUI supports:

```text
/help
/init
/settings
/login
/provider
/model
/thinking
/trust
/session
/tree
/checkout
/compact
/clear
/quit
```

`/help` lists these commands. `/login` opens a provider menu before hidden
credential input. `/provider`, `/model`, and `/thinking` open a value menu and
save the selected value directly to global settings. `/init` asks the current
model to scan the workspace and create or improve `AGENTS.md`; it requires
configured credentials and records a trusted decision for the workspace when
it creates the file. `/trust` saves a project trust decision for future runs. `/session` shows the
current Session file, `/tree` lists the Session branch tree and active leaf,
and `/checkout` chooses where the next Session branch starts. `/compact`
compacts the active branch at the current turn boundary. `/clear` clears the
visible transcript without changing Session history, and `/quit` exits AICE.
Menu-based commands do not accept typed arguments; choose values from their
menus instead. Successful setting changes update the current interactive
Session immediately. Trust decisions are applied on restart, not hot-reloaded.

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
