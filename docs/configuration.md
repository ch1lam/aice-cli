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

When `settings.json` omits `provider` and `model`, AICE uses `deepseek` and
`deepseek-v4-flash`. The `opencode-go` catalog default is also
`deepseek-v4-flash`.

| Setting | Environment variable | Supported values |
| --- | --- | --- |
| Provider | `AICE_PROVIDER` | `deepseek`, `opencode-go`, `openai`, `custom` |
| Model | `AICE_MODEL` | A model in the selected provider's catalog |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |
| Default Project Trust | none | `ask`, `always`, `never` |
| Custom base URL | `AICE_CUSTOM_BASE_URL` | OpenAI-compatible endpoint persisted as `custom_base_url`; default `http://localhost:11434/v1` |

### Thinking levels

`AICE_THINKING` uses seven canonical levels. Each model supports a subset,
declared by its provider catalog. AICE aligns an unsupported request to the
nearest supported level, preferring the next higher one and then the next
lower one. The effective level therefore always belongs to the selected
model's subset. The requested level remains saved so switching models can
restore it; `/settings` shows the effective level and `/thinking` lists only
valid choices for the active model. Models without thinking support expose
only `off`.

The default request is `medium`. On DeepSeek V4 Flash and Pro it becomes
`high`; on Kimi K3 it becomes `max`. Important built-in subsets are:

| Provider and model | Supported levels |
| --- | --- |
| `deepseek/deepseek-v4-flash`, `deepseek/deepseek-v4-pro` | `off`, `low`, `high`, `max` |
| `opencode-go/deepseek-v4-flash` | `low`, `high`, `max` |
| `opencode-go/deepseek-v4-pro` | `high`, `max` |
| `opencode-go/deepseek-v4-flash-vision-exp` | `off`, `low`, `high`, `max` |
| `opencode-go/kimi-k2.6` | `off`, `high` |
| `opencode-go/kimi-k3` | `max` |
| `opencode-go/glm-5.2` | `high`, `max` |
| `opencode-go/glm-5.3` | `low`, `high`, `max` |
| `opencode-go/gpt-5.6-luna` | `off`, `low`, `medium`, `high`, `xhigh`, `max` |
| `opencode-go/grok-4.5` | `low`, `medium`, `high` |
| `opencode-go/muse-spark-1.2-contributor` | `minimal`, `low`, `medium`, `high`, `xhigh` |
| `opencode-go/ox-alpha-free` | `low`, `high`, `max` |
| `opencode-go/hy3` | `off`, `low`, `high` |
| `openai/gpt-5.6*` | `off`, `low`, `medium`, `high`, `xhigh`, `max` |
| Other `opencode-go` models | `off`, `minimal`, `low`, `medium`, `high` |

`off` is a canonical switch, not necessarily a literal wire value. The
protocol adapters translate it to the provider's native form, such as
`thinking.type: "disabled"`, `enable_thinking: false`,
`reasoning_effort: "none"`, or an omitted effort field. Enabled levels also
resolve through the selected model's map before encoding. A direct request
that bypasses application clamping and names an unsupported level is rejected
rather than sent silently.

DeepSeek has three distinct enabled efforts: `low`, `high`, and `max`.
`medium` and `xhigh` are not separate choices because the upstream API folds
both into `high`. Its OpenAI-compatible shape sends `thinking.type` plus
`reasoning_effort`; the Anthropic shape sends `thinking` plus
`output_config.effort`; the Responses shape sends `reasoning.effort`, using
`none` for `off`.

### Model catalog metadata

Reasoning capabilities live with each model in the built-in provider catalogs,
not in protocol adapters. A model uses a tri-state map from canonical level to
provider token: a missing key uses the default mapping, a string supplies the
wire token, and an explicit `null` marks the level unsupported. Missing keys
for `off` through `high` are supported by default; `xhigh` and `max` require
explicit entries. Catalog copies deep-clone these maps so a Session or side
thread cannot mutate shared model data.

The catalogs are compiled into AICE and are never fetched at runtime. Pi AI is
the semantic reference for the tri-state map, while concrete capabilities and
wire formats follow provider documentation and gateway-specific requirements.
Update the model map, its wire-format metadata, and catalog assertions together
when upstream capabilities change.

The built-in OpenAI catalog intentionally stays small: `gpt-5.6`,
`gpt-5.6-terra`, and `gpt-5.6-luna`. `gpt-5.6-terra` is the default because it
balances model capability and cost. These models use the official Responses
API and support `off`, `low`, `medium`, `high`, `xhigh`, and `max` reasoning
levels.

The OpenCode Go catalog contains the 22 active upstream models; entries marked
deprecated upstream are omitted. GPT-5.6 Luna, Grok 4.5, and Muse Spark 1.2
Contributor use the Responses protocol. Qwen3.6 through Qwen3.8 and MiniMax
M2.7/M3 use Anthropic Messages; the remaining catalog uses Chat Completions.
Models whose upstream input modalities include images accept image content
through the programmatic LLM contract; the current TUI input remains
text-only.

OpenCode Go Chat Completions requests omit `max_tokens` when AICE has no
explicit output-token cap, allowing the gateway to choose its current default.
An explicit `MaxTokens` value, including one produced by context protection,
is still sent. Responses models use that protocol's normal output-token field.
Other providers keep sending their model default.

`default_project_trust` defaults to `ask`. Automation should use `--approve`
or `--no-approve` rather than a broad environment override. See [Project Trust
and prompts](project-trust.md) for protected resources and decision order.

## Credentials and connection overrides

Credentials are stored by provider in `~/.aice/auth.json` with file mode
`0600`. A process environment variable overrides the stored key.

| Provider | API key environment variable | Auth file key | Base URL override |
| --- | --- | --- | --- |
| DeepSeek | `AICE_DEEPSEEK_API_KEY` | `deepseek_api_key` | `AICE_DEEPSEEK_BASE_URL` |
| OpenCode Go | `AICE_OPENCODE_API_KEY` | `opencode_api_key` | `AICE_OPENCODE_BASE_URL` |
| OpenAI | `OPENAI_API_KEY` | `openai_api_key` | `AICE_OPENAI_BASE_URL` |
| Custom (Ollama, vLLM, LM Studio, any OpenAI-compatible) | `AICE_CUSTOM_API_KEY` | `custom_api_key` | `AICE_CUSTOM_BASE_URL` (default `http://localhost:11434/v1`) |

In the TUI, `/login` opens a provider menu. For a provider whose credential is
already available, the next menu explicitly offers either `Use saved
credential` (switch without entering a key) or `Enter a new API key` (replace
the saved key). Providers without a credential go directly to hidden input.
When a new key is entered, `/login` stores it in the auth file and also saves
the provider (and the effective model when the previous one does not belong to
that provider) to the global settings file, so the login survives a restart.
`/provider` remains the shorter command for switching to an already configured
provider. Missing credentials do not prevent the TUI from starting, but a
normal prompt asks the user to log in first.

Selecting `custom` starts a three-step hidden-input sequence: endpoint URL,
then API key, then model. Enter with an empty endpoint keeps
`http://localhost:11434/v1` (`custom.DefaultBaseURL`). The API key may be
empty (Ollama and similar local servers). Enter with an empty model keeps the
already stored model, or `llama3.1:8b` (`custom.DefaultModel`) when none is
stored. The endpoint is persisted as `custom_base_url` in `settings.json`.

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
--output-format      print output format: text or json (default text; requires --print)
--approve, -a        trust project-local resources for this run
--no-approve         ignore project-local resources for this run
--yolo               automatically allow tool calls that would otherwise ask; for isolated containers/CI; dangerous
--version, -v        show the version
```

`--print` requires exactly one prompt argument. Without `--session` it does
not persist the run. Session navigation and compaction commands are documented
in [Tool execution and Sessions](execution-sessions.md#sessions). `aice
update` is documented in [Installation and updates](installation.md).

The default `--output-format text` keeps answer text on stdout for shell
pipelines. Operational progress is written to stderr as one line per tool
start/end, retry start/end, and completed assistant message, followed by total
token usage. Tool completion status follows the paired tool result, so Guard
denials and tool failures appear as failures even when the event transport
itself succeeded.

`--output-format json` writes one NDJSON event per line to stdout and does not
duplicate progress on stderr. It is intended for integrations that need tool
arguments, results, durations, retries, stop reasons, and token usage. The
stable event contract is documented in [Print NDJSON events](contracts.md#print-ndjson-events).

## Agent Skills

A skill is a directory with a `SKILL.md` file (YAML frontmatter plus Markdown
instructions) that follows the open [Agent Skills](https://agentskills.io)
specification.

AICE loads skills from three sources at Session start:

1. **builtin** — embedded in the AICE binary
2. **user** — `~/.agents/skills/<name>/SKILL.md`
3. **project** — `<workspace>/.agents/skills/<name>/SKILL.md`

When two skills share a name, project wins over user over builtin. `/skills`
lists the catalog loaded for this Session; grouping is source information
only.

Install with:

```sh
npx skills add <owner/repo>
npx skills add -g <owner/repo>
```

`-g` installs into `~/.agents/skills/`. Any installer that writes a skill
directory under `.agents/skills/` works the same way.

Project-level skills are gated by Project Trust. An untrusted workspace skips
`<workspace>/.agents/skills/`; user-global and builtin skills are not gated.
See [Project Trust and prompts](project-trust.md).

At startup AICE injects only each skill's name and description into the
system prompt. The agent loads the body on demand through the `skill` tool.
Discovery and wiring are in [Skills](architecture.md#skills).

Skill directories on disk are allowed automatically for read-class tools
(`read`, `grep`, `find`, `ls`); `write` and `edit` are not granted. See
[Tool execution and Sessions](execution-sessions.md#tool-execution-boundary).

The `/skills` list is scanned at Session start. Installing or removing skills
takes effect after restarting AICE or starting a new Session.

## Interactive commands

| Command | Effect |
| --- | --- |
| `/help` | List commands |
| `/btw [question]` | Create or choose an ephemeral, tool-free side thread |
| `/init` | Create or improve root `AGENTS.md`; loaded after restart |
| `/settings` | Show effective model, Trust state, and configuration paths |
| `/skills` | List Agent Skills loaded for this Session |
| `/login` | Select a provider and store its key through hidden input; `custom` uses endpoint → key (may be empty) → model |
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
and are also saved globally. Press `?` for keyboard shortcuts. Session
navigation and compaction commands (`/session`, `/tree`, `/checkout`,
`/compact`) are detailed in [Tool execution and
Sessions](execution-sessions.md#resume-and-navigate).

### /btw

`/btw [question]` always creates a new side thread from a frozen snapshot of
the context AICE has already accepted. A bare `/btw` opens a chooser whose
first option creates a new thread and whose remaining options reopen live
threads; when none exist, it opens a blank composer without creating a thread
until the first question is submitted. Each thread keeps its own draft and
question/answer history.

Side threads run independently, without tools, so the main Agent run can keep
working. AICE retains at most five live threads, permits at most two side
answers at once, and keeps at most 20 interactions per thread. After an answer
terminates, its thread accepts follow-ups for 20 minutes. It then becomes
read-only but remains available for review until 120 minutes of inactivity,
when AICE permanently removes it from memory. Opening, viewing, hiding, or
editing an unsent draft does not reset these windows, and an answer already in
flight is allowed to finish before its idle clock restarts.

Side questions and answers do not enter the main transcript, prompt history,
Session JSONL, usage totals, or compaction input, and all disappear when AICE
exits. In a side panel, Enter asks a follow-up, Escape returns to the main view
without cancelling, Ctrl+C cancels only that side answer, and Ctrl+D ends the
thread; ending a running thread asks for confirmation and waits for its answer
to stop before deleting it.

## Interactive input delivery

The composer remains active while an Agent run is working:

| Input | Effect while an Agent run is active |
| --- | --- |
| `Enter` | Send a steer into the active run at its next safe boundary |
| `Ctrl+Enter` | Queue a follow-up interaction after the current one completes |
| `Shift+Enter`, `Alt+Enter`, or `Ctrl+J` | Insert a newline |
| `Ctrl+C` | Cancel the active response |

A waiting steer appears immediately in the transcript as a user message with
a distinct color and animated dashed rail. Queued prompts stay above the draft
inside the composer as indented `↳` previews, separated from the draft by a
blank line. Each multi-line queued prompt shows its first line followed by
`...`; multiple prompts remain in submission order. If the current interaction
reaches its natural stop before accepting a pending steer, AICE promotes that
steer to the follow-up queue instead of dropping it. Follow-ups stay inside
the same Agent run; the application persists each completed interaction as its
own Session turn.

## Operational environment variables

| Variable | Effect |
| --- | --- |
| `AICE_NO_DEP_INSTALL=1` | Do not download missing ripgrep or Windows Git Bash helpers |
| `AICE_NO_UPDATE_CHECK=1` | Disable the daily interactive update check |

Installation, helper provisioning, and self-update details live in
[Installation and updates](installation.md).
