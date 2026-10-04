# Configuration

## Settings and precedence

All settings resolve through an instance-local Viper registry, from highest
to lowest priority:

1. Explicit runtime selections (`Set`), including interactive changes.
2. Explicitly supplied command-line flags.
3. Supported environment variables.
4. Trusted project `.aice/settings.json`.
5. User files: `~/.aice/auth.json`, then `~/.aice/settings.json`.
6. AICE defaults.

This order covers provider, model, reasoning, endpoints, API keys, context
windows, default trust policy, and operational switches. Flags currently expose
`--provider`, `--model`, `--thinking`, `--no-dep-install`,
`--no-update-check`, `--run-token-budget`, `--run-timeout`,
`--run-no-progress-limit`, and `--max-turns`; an omitted flag does not override
another source.
Invocation controls such as `--workspace`, `--session`, `--approve`, and
`--yolo` retain their separate command semantics. No remote key/value store is used.

Computer Use preferences are User-only: `desktop_enabled` defaults to `false`
and `desktop_control_mode` defaults to `background_only` (the other value is
`foreground_allowed`). Only the user settings file and explicit runtime patches
can supply them. Auth files, trusted projects, environment and flag bindings
cannot change them or become reset/inheritance candidates. On macOS and Linux, enabling
in Settings opens the disclosed setup flow; the setup/repair action also offers
preference-only saving without native effects. Saving these keys alone does not
make a Driver ready. Linux setup checks X11 and asks for one window to capture
locally; it never sends that verification image to a model or installs system
packages. Wayland/XWayland routes and Windows setup remain unavailable.
See [Computer Use](desktop.md) for implemented actions and acceptance status.

Project settings are protected by [Project Trust](project-trust.md).
Until trusted, the project contributes no configuration values. The initial
trust decision uses only user files, environment variables, explicit trust
flags, and saved trust decisions; a project's own policy cannot authorize it.

Each file must be one JSON object. An unparseable file is skipped as a whole,
with a diagnostic, and lower layers remain available. Other read failures
remain errors. Types and business rules are checked after composition: a
valid higher-priority value masks an invalid lower-priority value; an invalid
winning value is an error, without falling back. Unknown effective fields
are errors. Missing and empty environment variables contribute no value;
present strings are trimmed. Boolean environment values accept Go's boolean
forms (`true`/`false`, `1`/`0`, `t`/`f` and their supported case variants).

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
`deepseek-flash`. The `opencode-go` catalog default remains
`deepseek-v4-flash`.

The DeepSeek API catalog contains only `deepseek-flash` (V4.1-Flash) and
`deepseek-v4-pro` (V4-Pro-0813). Only Flash accepts text/image input; Pro is
text-only. Flash retains Responses; Pro retains Anthropic Messages. The
OpenCode Go catalog is independent and unchanged.
Existing DeepSeek settings using removed model IDs must select one of these two
models; old IDs are not remapped.
DeepSeek cost estimates use official [off-peak rates](https://api-docs.deepseek.com/quick_start/pricing/);
peak billing is twice the estimate.

| Setting | Environment variable | Supported values |
| --- | --- | --- |
| Provider | `AICE_PROVIDER` | `deepseek`, `opencode-go`, `kimi-coding`, `moonshot`, `zhipu`, `zhipu-coding`, `openai`, `anthropic`, `anthropic-subscription`, `openai-codex`, `aihubmix`, `custom` |
| Model | `AICE_MODEL` | A catalog model, or any model ID for `custom` |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |
| Default Project Trust | `AICE_DEFAULT_PROJECT_TRUST` | `ask`, `always`, `never`; project values cannot grant startup trust |
| Custom base URL | `AICE_CUSTOM_BASE_URL` | OpenAI-compatible endpoint persisted as `custom_base_url`; default `http://localhost:11434/v1` |
| Context windows | `AICE_CONTEXT_WINDOWS` | JSON array of provider/model/token entries described below |
| Show browser window | `AICE_BROWSER_HEADED` | Boolean; file key `browser_headed`, default `false`; saved through `/browser` → Show window, see [Browser automation](browser.md#show-the-browser-window) |
| Disable helper downloads | `AICE_NO_DEP_INSTALL` | Boolean; file key `no_dep_install` |
| Disable startup update check | `AICE_NO_UPDATE_CHECK` | Boolean; file key `no_update_check` |
| Web search and fetch | none (file only) | Nested `web` object: search sources, priority, service instances, fetch switch; see [Web search and fetch](web.md#configuration) |

The `web` object and the `web_services` credential namespace in `auth.json`
are handled as whole objects per layer rather than field-merged. Only user
files and `/web` may define services, endpoints, credentials and priority;
trusted project settings may only set `web.search.enabled=false` or
`web.fetch.enabled=false`, and other project `web` content is ignored with a
diagnostic. Service keys referenced by `auth_ref` are stored under
`web_services.<id>` in `~/.aice/auth.json`; `env` references read the named
variable at startup.

### Run limits

| Flag | Settings key | Environment | Default |
| --- | --- | --- | --- |
| `--max-turns N` | `max_turns` | `AICE_MAX_TURNS` | `0` (unlimited) |
| `--run-token-budget N` | `run_token_budget` | `AICE_RUN_TOKEN_BUDGET` | `0` (unlimited) |
| `--run-timeout 30m` | `run_timeout` | `AICE_RUN_TIMEOUT` | `0s` (unlimited) |
| `--run-no-progress-limit N` | `run_no_progress_limit` | `AICE_RUN_NO_PROGRESS_LIMIT` | `8` identical tool rounds |

Token budgets must be non-negative integers; timeouts are non-negative Go duration
strings such as `30m` or `1h`. Explicit zero disables an inherited limit. Invalid
winning values fail configuration loading. Model/provider changes preserve the
loaded limits. Repetition limits accept `0` (disabled) or an integer of at least
`2`. The threshold counts consecutive identical tool rounds, not total model
rounds. `max_turns` accepts a non-negative integer; `0` leaves model rounds
unlimited. It counts model request attempts, including retries, within one run.

```sh
aice --max-turns 50 --run-token-budget 200000 --run-timeout 30m
aice --print "Fix the failing tests" --run-token-budget 100000
```

See [execution semantics](execution-sessions.md#run-resource-limits) for accounting,
compaction, overshoot, stopping and continuation.

### Interactive persistence and multiple instances

Interactive preference changes save only explicitly changed keys to
`~/.aice/settings.json`; API keys use `~/.aice/auth.json`, and OAuth uses separate
stores. Project files and unrelated inherited values are never written back.
Settings and slash commands share application operations in
[model_settings.go](../internal/app/model_settings.go).

A successful save publishes a runtime-priority snapshot in this instance. Active
runs and other processes retain their frozen settings; there is no file watcher
or whole-snapshot save on exit. On restart, flags, environment and project values
can override the saved preference again, and interactive commands report this.

Writers lock, reread, patch changed keys and atomically replace the target.
Independent peer changes survive; the last successful write to the same key wins.
Preference locks and Windows replacement retries have a cancellable five-second
bound. A crashed writer's lock is not automatically stolen. Malformed target
files are preserved and must be repaired before saving. Failed preference saves
leave the active selection unchanged. Credential and preference saves are separate
commits; errors distinguish credential-only success and post-commit cleanup
warnings. OAuth refresh has its own lock lifecycle below.

### Settings window

Open `/settings`. `/desktop` and `/mcp desktop` open Computer Use
in that same panel. Opening Settings creates no Session or Agent run.

| Area | Preferences or actions | Takes effect |
| --- | --- | --- |
| Models & Accounts | Provider/model/thinking, endpoints, context windows, API keys and OAuth | Next Agent run |
| Tools & Network | Browser, Computer Use, Web services and fetch, MCP | See the owning [browser](browser.md), [desktop](desktop.md), [Web](web.md), and [MCP](mcp.md) guides |
| Run Limits | Turns, tokens, timeout and repeated-tool limit | Next Agent run |
| Project & Trust | Default policy and saved project decision | Next startup |
| System | Helper downloads, update checks, paths and diagnostics | Next startup |

Tab/Shift+Tab switches categories; `/` searches; arrows select; Enter edits;
`?` opens details. Click a heading or use Left/Right to fold/unfold groups.
Booleans save immediately. Text, integers, durations and choices save with Enter.
Array editors use `a` to add, Enter to edit, Tab between cells, `d` to delete,
Ctrl+Up/Down to reorder, and Ctrl+S to save. Leaving a changed array offers
save/discard/keep. Escape backs out without cancelling a background response.

Details show effective values, saved preferences, inherited candidates and sources.
`D` writes the product default; `u` previews removal of a user override and Enter
confirms. Model inheritance resets the provider/model/thinking group. Reset uses
frozen startup layers, removes the corresponding runtime choices, and does not
reload another process's changes. “Known saved” is startup/latest-local-save data.

Shared-resource edits require idle main/BTW responses and no input preparation.
Restart-only preferences can still be saved during a run. The panel's **Stop
current run** action (F6) cancels and waits for completion before editing resumes;
Esc only closes or backs out. Resource changes reject previously prepared runs
and make old BTW snapshots read-only. Failed preparation or saving preserves
the prior runtime; committed changes survive closing the panel. Domain actions
report partial external effects separately. See [Settings lifecycle
contracts](contracts.md#settings-and-usage-capabilities).

Computer Use status loads separately through a bounded read-only check. Refresh
never starts a service, captures, or asks for OS grants. An enabled preference
alone does not mean ready. Setup and explicit task continuation are documented
in [Computer Use](desktop.md#settings-and-task-continuation). Settings drafts,
credentials and authorization responses never enter prompt or Session history.

### Usage and Session information

`/context`, `/usage`, and `/session` open Context, Session usage, and Session
info in the same window. The top-right context indicator supports a hover/click
format toggle. Tab changes pages and `r` refreshes. Select an information row
and Enter for scrollable text.

Context shows occupancy, capacity, estimate status, window source and model.
Session usage counts recorded input/output/cache tokens across every branch and
compaction, excluding temporary BTW answers. Reasoning tokens are an output
subset. Cost is **Unavailable**, **Partial estimate**, or **Estimate** according
to the original records' price coverage; current prices are never applied
retroactively. Missing reports from interrupted requests remain unreported.
This is not an account balance or subscription quota display.

Session info shows identity, path, directory, active leaf and node/message/
compaction counts. Opening any information window before the first prompt
shows “Not started” and creates no Session file. Reads are asynchronous and
cancelled on close. Generation checks reject stale window reads; lifecycle
completion or manual refresh updates the snapshot, never every streamed token.
The reader does not consume the one-time restored transcript in RuntimeState.

### Unavailable configured models

If a known provider's saved or environment-selected model is absent from its
catalog, interactive startup opens with a notice asking the user to select a
model via `/model`. The original ID remains visible; no replacement is selected
or saved automatically. Sending a message before selection repeats the notice
and preserves the draft, without reading attachments, calling the model, or
writing the prompt to Session history. `/btw`, `/init`, and model-based compaction
also require a valid selection. Selecting a valid model saves it and enables
requests when credentials are configured; `/provider` and `/login` retain their
existing provider-switch behavior. An `AICE_MODEL` environment override still
wins on the next startup and must be updated separately.

Non-interactive `--print` rejects an unavailable model and lists that provider's
available IDs. Unknown providers and invalid settings remain startup errors.
Custom providers continue accepting arbitrary IDs; availability is determined
within the selected provider, not by matching model names across providers.

### Context window and status bar

The header shows context occupancy as a percentage; hover previews used tokens /
capacity and clicking keeps that format for this TUI instance. Unknown values
show `?`; pressure turns amber at 70% and red at 90%. This is current context,
not cumulative Session usage. Unsent drafts and queued inputs are excluded.

After a successful response, occupancy uses matching provider/model usage plus
estimated accepted messages since that response. Before a response, after
compaction or after changing models, it estimates the prompt, tool definitions
and projected history. A new untouched conversation shows zero.

Capacity defaults to the selected provider/model catalog, even with an endpoint
override. Override it for the capacity actually enabled on your deployment:

```json
{
  "context_windows": [
    {"provider": "openai-codex", "model": "gpt-5.6-terra", "tokens": 272000},
    {"provider": "custom", "model": "Org/Model.v1", "tokens": 32768}
  ]
}
```

Entries match exact, case-sensitive provider/model IDs; token counts must be
positive and pairs unique. The winning configuration layer replaces the whole
array; an empty array clears inherited overrides. `/settings` edits it and shows
the resolved capacity/source. Restart after editing files. Overrides survive
interactive model changes and govern request protection and compaction too.

AICE does not probe account entitlements or enable server-side context tiers.
`custom` uses a 128,000-token fallback until overridden; it is not a discovered
endpoint limit. Verify capacity again after changing endpoints or accounts.

### Thinking levels

`AICE_THINKING` uses seven canonical levels. Each model supports a subset,
declared by its provider catalog. AICE aligns an unsupported request to the
nearest supported level, preferring the next higher one and then the next
lower one. The effective level therefore always belongs to the selected
model's subset. The requested level remains saved so switching models can
restore it; `/settings` shows the effective level and `/thinking` lists only
valid choices for the active model. Models without thinking support expose
only `off`.

The default request is `medium`. `/thinking` shows the selected model's actual
choices; `/model` shows its compiled catalog. Capabilities are maintained in
[provider catalogs](../internal/provider), rather than copied into a second
model table here.

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

Catalogs are compiled into AICE, not fetched at runtime. Each model owns protocol,
modalities, context/output budgets, price estimates and a tri-state thinking map:
a missing key uses the default mapping, a string maps to a wire token, and `null`
is unsupported. Missing `off`–`high` entries are supported by default; `xhigh` and
`max` require explicit entries. Equivalent mappings collapse into one choice.
Update catalogs and their tests together; protocol adapters only encode them.
See [model contracts](contracts.md#messages-and-model-boundary).

Model availability, remote limits and billing can differ from compiled metadata.
Costs are estimates and do not model every service tier, long-context surcharge,
promotion or subscription quota. Offline adapter tests do not establish live
account/model access.

OpenCode Muse Spark can reject stored encrypted reasoning after its upstream
context changes. After an actual recognized rejection, the Loop retries once
with reasoning history projected to plain text for the rest of that run. It
preserves effort, tool history and original Session signatures; it does not
rewrite the transcript.

### Client identity and subscription use

Protocol adapters identify AICE as `User-Agent: aice/<version>` (`aice/dev` for
unstamped builds), without account, host, workspace or prompt data. Claude OAuth
has the compatibility headers described below. There is no user-facing identity
override. Use the dedicated subscription provider and authorized credentials;
changing a `custom` endpoint does not add subscription-specific routing.

OpenCode Go sends `x-opencode-session` across all three protocols. The app uses
the stored Session ID across turns, retries, model changes, compaction and resume;
`/new`, stateless print and individual BTW threads get separate identities.
Direct callers may use `llm.WithSessionID`; otherwise the provider uses an ID
stable for that provider instance. Proxies must preserve these headers.
Chat Completions and Responses omit the output cap when none was explicitly
requested; explicit caps are sent. Other providers retain their own policies.

`custom` always uses Chat Completions and accepts arbitrary model IDs, text/images,
a 128,000-token fallback context and 16,384-token output budget. These are local
metadata defaults in [custom.go](../internal/provider/custom/custom.go), not
capability discovery. Unsupported inputs/parameters surface as server errors;
AICE does not strip them and retry. Missing pricing does not imply free usage.

Model retries belong to the [Agent Loop](contracts.md#agent-loop); they are not
an account-wide rate limiter. Subscription eligibility, quota and billing remain
controlled by the provider; client headers alone do not establish approval.

## Credentials and connection overrides

API keys are stored by provider in `~/.aice/auth.json` with file mode
`0600`. Keys and endpoints follow the same precedence as other settings.
The auth file normally holds credentials, but both JSON files share the full
schema. For each row below, the file endpoint key replaces `_api_key` in the
auth key with `_base_url`, for example `openai_base_url`. Codex and Claude subscription OAuth
credentials each use a separate file as described below.

| Provider | API key environment variable | Auth file key | Base URL override |
| --- | --- | --- | --- |
| DeepSeek | `AICE_DEEPSEEK_API_KEY` | `deepseek_api_key` | `AICE_DEEPSEEK_BASE_URL` |
| OpenCode Go | `AICE_OPENCODE_API_KEY` | `opencode_api_key` | `AICE_OPENCODE_BASE_URL` |
| Kimi Coding Plan | `KIMI_API_KEY` | `kimi_api_key` | `AICE_KIMI_BASE_URL` |
| Moonshot API (China) | `MOONSHOT_API_KEY` | `moonshot_api_key` | `AICE_MOONSHOT_BASE_URL` |
| Zhipu Coding Plan | `ZHIPU_CODING_API_KEY` | `zhipu_coding_api_key` | `AICE_ZHIPU_CODING_BASE_URL` |
| Zhipu API (China) | `ZHIPU_API_KEY` | `zhipu_api_key` | `AICE_ZHIPU_BASE_URL` |
| OpenAI | `OPENAI_API_KEY` | `openai_api_key` | `AICE_OPENAI_BASE_URL` |
| Anthropic (Claude API) | `ANTHROPIC_API_KEY` | `anthropic_api_key` | `AICE_ANTHROPIC_BASE_URL` |
| AiHubMix | `AIHUBMIX_API_KEY` | `aihubmix_api_key` | `AICE_AIHUBMIX_BASE_URL` |
| Custom (Ollama, vLLM, LM Studio, any OpenAI-compatible) | `AICE_CUSTOM_API_KEY` | `custom_api_key` | `AICE_CUSTOM_BASE_URL` (default `http://localhost:11434/v1`) |

Settings login and `/login` use one application coordinator. Credential and
preference writes remain separate; failures report what committed. An API-key-only
save leaves current clients unchanged. A changed OAuth credential also invalidates
prepared runs and BTW snapshots because subscription clients reread it on each
request. See [lifecycle contracts](contracts.md#settings-and-usage-capabilities).

In the TUI, `/login` first offers `Sign in with an account` or
`Sign in with an API key`, then a provider menu. Confirm each menu level before
proceeding; text filters only the current menu. `/login` does not accept inline
provider/endpoint/model configuration. For an API-key provider whose credential is
already available, the next menu explicitly offers either `Use saved
credential` (switch without entering a key) or `Enter a new API key` (replace
the saved key). Providers without a credential go directly to hidden input.
When a new key is entered, `/login` stores it in the auth file and also saves
the provider (and the effective model when the previous one does not belong to
that provider) to the global settings file, so the login survives a restart.
`/provider` remains the shorter command for switching to an already configured
provider. Missing credentials do not prevent the TUI from starting, but a
normal prompt asks the user to log in first.

Custom supports keyless servers, so its credential menu appears even without
a saved key. Choose `/login` → `Sign in with an API key` → `Custom` →
`Enter a new API key` to start three hidden inputs: endpoint URL, API key,
then model. `Use saved credential` switches without these inputs.
An empty endpoint keeps the current effective custom URL, falling back to
`http://localhost:11434/v1` only when none is configured. An empty API key
clears the saved custom key for keyless servers such as Ollama. An empty model
keeps the current effective model ID, even when switching from another provider;
when no model is configured, it uses `llama3.1:8b`. Enter the intended local
model explicitly when switching providers. A supplied endpoint is persisted as
`custom_base_url` in `settings.json`.

For non-interactive setup, send the key on standard input so it does not appear
in command-line arguments:

```sh
printf '%s\n' "$OPENAI_API_KEY" | \
  aice config set-key --provider openai
```

Provider keys are stored side by side; updating one does not erase another.

### Anthropic (Claude API)

Choose `/login` → `Sign in with an API key` → `Anthropic (Claude API)`, or:

```sh
ANTHROPIC_API_KEY="your-key" aice --provider anthropic --model claude-sonnet-5
```

This uses the separately billed Messages API. `AICE_ANTHROPIC_BASE_URL` is the
API root: omit `/v1/messages` and `/v1`, which the SDK appends. The default model
is `claude-sonnet-5`; [anthropic.go](../internal/provider/anthropic/anthropic.go)
owns models, image support, budgets and thinking choices. `/model` and `/thinking`
expose those choices. Claude Pro/Max uses the separate OAuth provider below.

### Claude subscription (Pro/Max OAuth)

Select `/login` → `Sign in with an account` → `Claude Pro/Max` → `Browser login`.
AICE opens the authorization page and receives the callback on
`127.0.0.1:53692` (`http://localhost:53692/callback`). The TUI also accepts a
pasted authorization code or callback URL. If another login owns that port,
finish or cancel it before retrying. Escape or Ctrl+C cancels the login and
closes the callback listener. Terminal commands print a URL and wait for the
browser callback; there is no device-code flow.

```sh
aice auth login --provider anthropic-subscription
aice auth status --provider anthropic-subscription
aice --provider anthropic-subscription --model claude-sonnet-5
aice auth logout --provider anthropic-subscription
```

Successful login saves the provider and a compatible model; TUI login also
activates them in the current Session. `/provider` or `Use saved credential`
switches back without authorizing again. This provider shares the Claude API
model catalog and thinking choices above; model access, context limits and
subscription quota depend on the account. AICE does not quote API prices for
subscription requests or infer remaining quota from token usage.

AICE implements the native PKCE OAuth and Messages protocol used by
[Pi's Anthropic OAuth implementation](https://github.com/badlogic/pi-mono/blob/d6af72e1857cfb10b41d8ff8e69f0d72b4cf6d31/packages/ai/src/auth/oauth/anthropic.ts).
It needs no Claude Code executable and does not import another harness's
credentials. The compatibility request uses Pi's OAuth client ID, required
Claude CLI headers/system preamble and tool-name casing; the Messages adapter
maps tool names back before AICE's Agent Loop and Guard see them. AICE retains
ownership of tools, permission checks, history and cancellation. Ordinary API
requests keep AICE's client identity and use their own API key.

Credentials live only in `~/.aice/claude-subscription-auth.json` (mode `0600`).
A bounded cross-process lock serializes login, logout and token refresh;
each request rereads the file and refreshes within five minutes of expiry.
Rotated tokens are atomically saved before use, and failed refresh preserves
the previous file. Logout removes only this file; it stops new requests but
does not revoke an already running request. API keys and Codex credentials
remain independent. `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and
`AICE_ANTHROPIC_BASE_URL` cannot override subscription authentication or routing;
there is no automatic fallback to billable API access.

This is third-party compatibility with Pi, not an officially supported
Anthropic integration. Anthropic's [authentication rules](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use)
restrict subscription OAuth use to its own products; availability can change.
OpenCode [removed its built-in Anthropic auth plugin](https://github.com/anomalyco/opencode/pull/18186),
so its current main branch is not the implementation reference here.
Offline tests cover login, refresh, streaming tools and replay; live account
acceptance and subscription billing have not been verified.

### AiHubMix

Use `/login` → API key → AiHubMix, or `AIHUBMIX_API_KEY` with
`--provider aihubmix`. The default is `gpt-6-sol`, with API root
`https://aihubmix.com/v1`. `AICE_AIHUBMIX_BASE_URL` includes `/v1` but excludes
`/responses`, `/messages` and `/chat/completions`.

The curated [catalog](../internal/provider/aihubmix/models.go) selects Responses
for GPT, Messages for Claude and Chat Completions for DeepSeek/Kimi. It is not
remote discovery. Arbitrary IDs use `custom` with generic Chat Completions
metadata. Offline fixtures cover protocol/auth/thinking/replay; live account
access has not been verified.

### Kimi Coding Plan

Use `/login` → API key → Kimi Coding Plan, or `KIMI_API_KEY` with
`--provider kimi-coding`. It uses `https://api.kimi.com/coding/v1/responses`
through Responses, independently of Moonshot API credentials.

The default is `kimi-for-coding`; membership controls access to the compiled
[catalog](../internal/provider/kimi/kimi.go). Its conservative context default is
262,144 tokens and AICE output budget is 32,768. If your account enables K3's 1M
tier, add `{"provider":"kimi-coding","model":"k3","tokens":1048576}` to
`context_windows`. Usage is recorded; zero per-token estimates do not imply
unlimited quota.

### Moonshot API Platform

Use `/login` → API key → Moonshot API, or `MOONSHOT_API_KEY` with
`--provider moonshot`. The China API root is `https://api.moonshot.cn/v1` and
the default model is `kimi-k3`. `KIMI_API_KEY` never supplies its credential.
`AICE_MOONSHOT_BASE_URL` is an explicit endpoint override, not region detection.

All compiled [Moonshot models](../internal/provider/moonshot/moonshot.go) use
Responses with text/image input; there is no Chat Completions fallback. This
uses prepaid API billing, separately from Coding Plan quota. CNY prices are
not converted into AICE's USD estimates; consult platform billing for charges.

### Zhipu API Platform

Use `/login` → API key → Zhipu API, or `ZHIPU_API_KEY` with `--provider zhipu`.
The API root is `https://open.bigmodel.cn/api/paas/v4`; the default model is
`glm-5.3`. `AICE_ZHIPU_BASE_URL` excludes `/chat/completions`.

The [catalog](../internal/provider/zhipu/models.go) declares model-specific image
and thinking support; `/model` and `/thinking` expose its current choices.
Requests use Chat Completions with same-model reasoning replay. Video, file,
speech and specialized output APIs are not implemented. This is the separately
billed China API; CNY prices are not converted into AICE's USD estimates.

### Zhipu Coding Plan

Use `/login` → API key → Zhipu Coding Plan, or `ZHIPU_CODING_API_KEY` with
`--provider zhipu-coding`. Its API root is
`https://open.bigmodel.cn/api/coding/paas/v4`, overridable with
`AICE_ZHIPU_CODING_BASE_URL`. The catalog contains `glm-5.3` (default) and
`glm-5.3-flash`; it is separate from the API Platform catalog.

Neither credential nor endpoint falls back to `zhipu`, including on quota
exhaustion. Subscription requests retain AICE's identity and zero per-token
estimates, which do not imply unlimited quota or platform eligibility. Live
credential acceptance has not been verified.

### Codex subscription (ChatGPT OAuth)

`openai-codex` uses ChatGPT subscription access, independently of the `openai`
API-key provider. In the TUI, choose `/login` → `Sign in with an account` →
`OpenAI Codex` → `Browser login (default)` or `Device code login (headless)`.
Browser login opens the authorization page and displays its clickable URL.
Complete login in the browser, or paste the authorization code / redirect URL
into the hidden input. AICE must acquire callback port 1455 before opening the
browser. If another login (such as pi or Codex) owns the port, AICE stops and
asks you to cancel that login and retry, or select device code login. It never
opens an authorization URL after failing to acquire the callback listener.
Device login displays the verification URL and code while waiting.
Escape or Ctrl+C cancels either flow and restores the composer. Authentication
prompts and pasted codes are transient; they never enter Session or prompt history.
Successful login saves credentials and activates Codex in the current Session.

The standalone terminal command is also available:

```sh
aice auth login --provider openai-codex
```

Open the printed URL in a browser on the same machine. AICE verifies the
OAuth state and PKCE exchange through a loopback callback on port 1455.
For SSH/headless machines or an occupied callback port, use:

```sh
aice auth login --provider openai-codex --device-code
aice auth status --provider openai-codex
aice auth logout --provider openai-codex
```

Device login prints a verification link and one-time code. It must be enabled
in ChatGPT security settings or workspace permissions; see
[OpenAI authentication guidance](https://learn.chatgpt.com/docs/auth).
Both login methods time out after 15 minutes and cancel with Ctrl+C. Login
saves the provider and a compatible model globally; `AICE_PROVIDER` and
`AICE_MODEL` still take precedence at startup. To reuse a saved login, choose
`Use saved credential` under the Codex login menu or select Codex in `/provider`.

The compiled catalog contains `gpt-6-astra`, `gpt-5.6-sol`,
`gpt-5.6-terra` (default), and `gpt-5.6-luna`. These are subscription models
checked on 2026-09-07 against
[OpenAI's Codex model guide](https://learn.chatgpt.com/docs/models); availability
and usage limits depend on the account. AICE records token usage with zero
per-token API price estimates for this provider. This does not mean unlimited
or free usage. Default context/output metadata is 272,000/128,000 tokens;
the subscription endpoint chooses the actual output limit and AICE omits
`max_output_tokens`, including explicit local output caps, because that field
is unsupported. AICE's local context accounting and compaction still apply.

AICE stores access/refresh tokens, account ID, and expiry in
`~/.aice/codex-auth.json` (mode `0600`, atomic replacement), without reading
or modifying Codex CLI or pi credentials. It refreshes within one minute of
expiry and saves rotated tokens before making a model request. Concurrent
AICE processes serialize refresh/login/logout with `codex-auth.json.lock`.
Lock waits are cancellable and bounded to one minute. On Windows, access-denied
errors while creating the lock are retried within that bound to tolerate
transient filesystem contention. Credential replacement also retries access-denied
and sharing-violation errors while holding the lock, within the same deadline.
If waiting fails, the error preserves both the cancellation/deadline and the last
filesystem error so persistent permission failures remain visible. After a crash,
remove that stale lock directory only when no AICE process is running. Failed refresh
preserves the prior credential; expired/revoked authorization requires logging
in again. Logout removes only AICE's Codex credentials: in-flight requests may
finish, but subsequent requests fail closed. It does not revoke the account's
server-side sessions or change the selected provider.

Subscription requests use `https://chatgpt.com/backend-api/codex/responses`,
SSE, `store: false`, and encrypted reasoning replay. `OPENAI_API_KEY` and
API/custom base URL overrides never apply to this provider. OAuth and wire
compatibility follow the public
[pi implementation at commit 9767ba2](https://github.com/badlogic/pi-mono/tree/9767ba275f3e9a5ee0f5c5342249b629ab1b2282/packages/ai/src).
This is a subscription compatibility endpoint rather than the public API
billing endpoint; upstream protocol changes may require an AICE update.

## Command-line options

```text
aice [--print <prompt>] [flags]

--workspace <path>   working directory for Agent tools (default .)
--session <path>     Session JSONL file to create or resume
--print, -p          print one response and exit
--output-format      print output format: text or json (default text; requires --print)
--provider <id>      override the provider for this invocation
--model <id>         override the model for this invocation
--thinking <level>   override the requested thinking level
--no-dep-install    disable automatic helper downloads
--no-update-check   disable the interactive startup update check
--max-turns         model request attempts per Agent run, including retries (0: unlimited)
--run-token-budget  provider-reported token budget per Agent run (0: unlimited)
--run-timeout       wall-clock budget per Agent run, e.g. 30m (0: unlimited)
--run-no-progress-limit  consecutive identical tool rounds before stopping (default: 8; 0: disabled)
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

AICE loads skills from three sources when preparing the process's run environment:

1. **builtin** — embedded in the AICE binary
2. **user** — `~/.agents/skills/<name>/SKILL.md`
3. **project** — `<workspace>/.agents/skills/<name>/SKILL.md`

When two skills share a name, project wins over user over builtin. `/skills`
lists the catalog loaded for this Session; grouping is source information
only. Settings → Project → Skills shows the same startup catalog grouped by
source, with each skill name, full description and location on separate lines.
The TUI highlights names and source headings, separates diagnostics and restart
notes, and wraps long descriptions and paths in the scrollable detail view.

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
In the main composer, type `/` at the beginning of any line or after whitespace
and search a Skill name or description. Skills appear alongside commands as
`/skill:<name>` entries. Tab or Enter inserts a green `[skill:name]` reference at
the cursor, preserving the surrounding text, files, images and long pastes.
Selection does not send. References move and delete as whole blocks, including
when visually wrapped. File references remain gold and image/paste tokens blue.
Multiple skills can appear anywhere in the draft; sending includes each distinct
skill once, up to eight per input. Skills can also be selected while composing
steering or follow-up input during a response; ordinary commands require idle.

On send, the application validates exact names against the Trust-filtered startup
catalog and uses the Guard and existing `skill` loader to attach the full body,
base directory and resource listing. Resource file contents are still read on
demand. The accepted user message persists those instructions in Session history;
loading failure restores the draft and its references. Selection does not enable
tools or grant permissions. Typing `/skill:<name> <task>` at the start and sending
also attaches that skill. Manually typed or pasted `[skill:name]` text alone is
literal and cannot create a binding. External editing retains unambiguously
surviving, previously attached labels; copied/ambiguous labels remain literal.
Like image drafts, drafts with skill attachments do not navigate prompt history
with Up/Down; recalled historical labels are plain text and can be reselected.

`/skills` lists the startup catalog. Shortcut names escape special characters and
disambiguate case-only collisions; select the displayed entry for those names.
These references are an interactive composer feature; `--print` accepts ordinary
requests to use a named Skill.
The builtin `browser` skill describes browsing through `bash` and `read`; see
[Browser automation](browser.md) for installation and connection requirements.
The builtin `computer-use` guide is pinned to Cua Driver 0.30.4 and applies when
`managed:cua` is available. It uses normal on-demand activation and source
shadowing; loading guidance neither enables Computer Use nor grants MCP access.
Enabled production main runs expose only the managed discovery route; see [Computer Use](desktop.md#managed-mcp-boundary).
Discovery and wiring are in [Skills](architecture.md#skills).

Skill directories on disk are allowed automatically for read-class tools
(`read`, `grep`, `find`, `ls`); `write` and `edit` are not granted. See
[Tool execution and Sessions](execution-sessions.md#tool-execution-boundary).

Restart AICE after installing or removing skills. `/new` resets Session
history but reuses the startup skill catalog, tools, and prompt; it does not
rescan skills. The `/skills` reminder reports that restart requirement.

## Interactive commands

The empty conversation shows AICE artwork, version/update status and rotating
usage tips when terminal space permits. They never enter conversation history.

### Visual theme

[theme.go](../internal/tui/theme.go) owns the built-in ink palette. AICE paints its
canvas without changing terminal defaults; transparency/blur remain terminal
settings. Status labels, icons and diff signs remain meaningful without color.
The composer retains the real terminal cursor for IME composition.

### Commands

| Command | Effect |
| --- | --- |
| `/help` | List commands |
| `/btw [question]` | Create or choose an ephemeral, tool-free side thread |
| `/init` | Create or improve root `AGENTS.md`; loaded after restart |
| `/settings` | Open the five-category Settings window |
| `/desktop` | Open Computer Use in the same Settings window |
| `/context`, `/usage` | Open current context or recorded Session usage |
| `/browser` | Browser status, connection, tab selection and close; `/browser status` also works |
| `/mcp` | Manage MCP services, connection approval and credentials; see [MCP](mcp.md) |
| `/web` | Web search services, priority order, credentials and the `web_fetch` switch; see [Web search and fetch](web.md#the-web-command) |
| `/skills` | List Agent Skills loaded for this Session |
| `/skill:<name> [task]` | Attach a discovered Skill and load its full instructions on send |
| `/login` | Choose account or API key, then provider and credential action; see [login flows](#credentials-and-connection-overrides) |
| `/provider` | Select and save the global provider |
| `/model` | Select and save a model from that provider |
| `/thinking` | Select and save a supported reasoning level |
| `/trust` | Save a Trust choice for restart; temporary choices are available only at startup |
| `/session` | Open Session information in the Usage window |
| `/history [id]` | Browse conversation history in the current project (also Ctrl+R, labeled `history`; F2 renames the selected session, an empty title restores the first question), or restore an existing local session by filename stem |
| `/tree` | Show all Session branches |
| `/checkout` | Select where the next branch starts |
| `/compact` | Append a compaction checkpoint for the active branch |
| `/new` | Detach from the current Session; the next prompt starts fresh |
| `/clear` | Clear the viewport without changing Session history |
| `/quit` | Exit AICE |

Type `/` at a word boundary to search commands and Skills. Up/Down selects,
Tab completes, Enter chooses, and Escape closes or returns to the parent menu.
Typing after a menu command filters its current options. Selecting a command
inside a draft temporarily preserves surrounding text and attachments, runs that
command separately, then restores the draft; surrounding prose is not arguments.
Skill selection inserts an attachment without sending. Press `?` in an empty
main/BTW composer to expand available shortcuts.

Session navigation is documented in [Execution and
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
exits. In a side panel, Enter asks a follow-up, Escape cancels the visible side
answer (or closes the panel when idle), and Alt+Escape returns to the main
view without cancelling. Ctrl+C clears the visible editor; a consecutive
second press exits AICE. Ctrl+D ends the thread; ending a running thread asks
for confirmation and waits for its answer to stop before deleting it.

## Interactive input delivery

Process, tool-batch, thinking and tool-detail folds are independent. Process and
batch summaries begin open, thinking/tool bodies closed. Final answers stay
outside process folds; manual fold choices survive streaming. Click a heading
to toggle it; `Ctrl+O` toggles all main process details. Streaming thinking shows
a 4 KiB tail until completion. Folding changes only presentation.

Drag transcript text to copy on release. Click the header directory to copy its
absolute path; Command-click (macOS) or Ctrl-click (Windows/Linux) opens it in
the system file manager if the terminal forwards that gesture. Composer clicks
highlight the frame but do not position the caret; use keyboard editing.
See the [known textarea limitation](maintenance.md#composer-click-positioning-and-textarea-capabilities).

Shortcut help follows the focused window. Dialog keys cannot trigger background
conversation actions; stale asynchronous paste replies are rejected after focus
changes or draft clearing. Rendering ownership and shared paint/hit-test geometry
are in [Concurrency and TUI](contracts.md#concurrency-and-tui).

### Tool output and code panels

Expanded tools show recorded output, never reread files. Previews are bounded to
64 KiB / 2000 source lines with explicit notices. Empty, unavailable and non-text
output remain distinct. Code panels highlight by language/path, escape terminal
controls, number original lines and preserve source independently of wrapping.

Completed main-answer code over 80 lines or at least 8 KiB starts with a 12-line
preview. Click its status row or use `Alt+O` (history also accepts `C`) to toggle
the first visible expandable block. Search reveals hidden matching code.
`[Copy]` copies the entire supplied source even while folded; a source-line click
copies that original line without its final newline. Limited tool previews can
copy only their retained source. Dragging still selects visible text.

Successful writes/edits show recorded unified diffs and added/removed counts.
Counts come from the full bounded alignment, not requested arguments; unknown
counts and omitted diff output are marked incomplete. Older records never acquire
a reconstructed diff. Before execution, writes may show a bounded argument
preview labelled `not executed`; it grants no authority. Display limits never
limit file mutations or rewrite Session content.

### Sending input while working

The composer remains active while an Agent run is working:

| Input | Effect while an Agent run is active |
| --- | --- |
| `Enter` | Send a steer into the active run at its next safe boundary |
| `Ctrl+Enter` | Queue a follow-up interaction after the current one completes |
| `Shift+Enter`, `Alt+Enter`, or `Ctrl+J` | Insert a newline |
| `Esc` | Cancel the active response, preserving the draft |
| `Ctrl+C` | Clear the editor; press again consecutively to exit AICE |

In the main and side composers, the first `Ctrl+C` clears all unsent text,
long-paste placeholders, and image attachments without adding a notice row.
This first press never cancels generation or exits, even when the editor is already
empty. A consecutive second `Ctrl+C` on the empty editor exits; another key,
text paste, mouse press/wheel, terminal blur, or input-domain change resets the
sequence. Streaming output and timers do not reset it. Clearing a draft does not remove queued inputs
or conversation history. Exiting uses normal shutdown to cancel and wait for
active runs. `Ctrl+D` also exits from an empty, idle main composer.

Menus, login prompts, and tool approval dialogs keep their contextual cancel
or deny behavior; they do not count as the first press of this exit sequence.

A waiting steer appears immediately in the transcript as a user message with
a distinct color and animated dashed rail. Queued prompts stay above the draft
inside the composer as indented `↳` previews, separated from the draft by a
blank line. Each multi-line queued prompt shows its first line followed by
`...`; multiple prompts remain in submission order. If the current interaction
reaches its natural stop before accepting a pending steer, AICE promotes that
steer to the follow-up queue instead of dropping it. Follow-ups stay inside
the same Agent run; the application persists each accepted source message
individually. See [Sessions](execution-sessions.md#sessions) for failure and
recovery behavior.

A paste larger than the visible composer collapses into an inline placeholder
(`[first words·Nlines]`) that reads like ordinary text. The cursor treats it
as one unit: left/right jump over it and one Backspace or Delete removes the
whole paste. Surrounding text stays in place, so `AAAA` + paste + `bbb` keeps
its order. `Ctrl+G` opens the expanded draft in `$VISUAL`/`$EDITOR` (fallback
`vi`); GUI editors must block (e.g. `code --wait`). A missing editor binary
reports which command was tried and how to set `VISUAL`/`EDITOR`; saving refills the composer as plain text that scrolls normally
instead of collapsing again. `Enter` always sends the expanded text, and
pasted content is sent literally, never parsed as a slash command. History
and thread drafts keep the expanded text.

### Asking the user (Q&A)

The interactive-only `request_user_input` tool asks 1–3 questions with at most
3 options each. It is for consequential missing requirements/preferences, not
permissions, credentials or information available from the repository.

Up/Down moves focus; Left/Right switches questions. Space or a digit selects;
typing supplies a custom answer or supplement. Enter confirms and advances, then
submits when every question is answered. Focus and recommendations never count
as answers. Empty custom answers keep the question open; answers are limited to
2,000 characters. PgUp/PgDown scroll the active answer/question view.

Esc browses the conversation while retaining the answer draft; Ctrl+C cancels
the run. The previous composer and attachments remain intact. Question input
cannot trigger slash commands, file expansion or steering. Cancellation retains
no partial answers; only submitted results enter Session history. Print, Harbor
and BTW runs have no question tool.

### Clipboard images

In the main composer, `Ctrl+V` or `Alt+V` reads an image from the system
clipboard, falling back to text when there is no image. `Alt+V` is useful when
Windows Terminal intercepts `Ctrl+V`. The terminal's normal paste shortcut
continues to handle text; it does not transport clipboard image bytes.

Images appear at the cursor as inline `[Image 1]`, `[Image 2]` placeholders.
Like long text placeholders, arrow keys cross the whole token and
Backspace/Delete removes the token and its image together. Send images with a caption or alone. `Enter` and `Ctrl+Enter` also carry
images in steering and follow-up inputs. Rejected submissions preserve the
text and images and display the reason. Choose a model with image input;
text-only models do not silently discard attachments.

PNG and JPEG are accepted, with at most four images per input. The shared
`internal/media` processor accepts originals up to 16 MiB, 8000 pixels per
side, and 16 megapixels. Images are automatically reduced, preserving aspect
ratio, to at most 2000 pixels per side and 3 MiB encoded bytes. PNG is preferred;
byte-heavy views may be encoded as JPEG on white. All image bytes in an input,
including retained originals, are limited to 32 MiB. These are AICE limits;
providers may impose additional limits across the conversation.

Processed images retain their source bytes when changed and a content-derived
identifier. The existing Session JSONL stores both the view and its original,
so restoration does not depend on the clipboard or an unchanged source file.

On macOS the built-in `osascript`/AppKit bridge reads PNG or converts clipboard
TIFF to PNG. Windows uses Windows PowerShell and the system clipboard to encode
PNG. Linux uses `wl-paste` on Wayland or `xclip` on X11 (install the relevant
helper separately). Clipboard commands run on the AICE host, so a remote SSH
session does not read your local desktop clipboard. No helper is auto-installed.

Image content is saved inline in the existing Session JSONL, so resuming does
not require the source file or clipboard. The transcript shows attachment
labels rather than image previews. Prompt-history recall retains text only;
while a draft has images, arrow keys edit its text. The external editor edits
expanded text with image placeholders preserved; deleting an image placeholder
in the editor removes that attachment. Send or remove images before running
slash commands. `/btw` supports ordinary pasted text; image attachments are supported in the
main conversation only.
The `read` tool accepts PNG/JPEG/GIF/WebP/BMP files through the same image processor.
GIF, static WebP (lossy, lossless, and alpha), and supported BMP variants are
converted to PNG, with the existing JPEG-on-white fallback and byte limit.
GIF uses only the first frame on the full original canvas; unpainted pixels
start transparent (white in a JPEG view).
Later animation frames are not decoded or shown. The model receives an explicit
conversion note and, for GIF, a first-frame note, including on saved-original
reads and crops. Animated WebP is rejected because the decoder does not support
animation: extract the desired frame as PNG and read that file. BMP variants
unsupported by the decoder, such as RLE compression, report an explicit error.
Invalid images report the decoder reason and advise re-exporting PNG/JPEG or
extracting the desired frame before reading again, instead of retrying unchanged
input. Exact source bytes remain in Session history when converted. The tool
returns an image content block, including a stable `image:<sha256>` identifier
and original-to-view coordinate mapping in model requests. Use `image_id`
instead of `path` to re-read that saved original, even after the source file
changes or disappears. An optional `crop` object (`x`, `y`, `width`, `height`)
selects original pixels before resizing, allowing detailed inspection. Text
`offset`/`limit` cannot be combined with an image; non-vision models receive a
tool error rather than an omitted image. `read` also supports shallow directory
listings, bounded by 2000 entries or 50 KiB.

### File references

Use `@src/main.go` or `@"images/screen shot.png"` in the main composer or a
`--print` prompt to attach a file. References start at a whitespace boundary;
email addresses, `@@literal`, and backtick code remain ordinary text. Use quotes
for paths with spaces. Relative paths resolve from the workspace; `~` and
absolute paths are supported. Opaque long-paste placeholders are not scanned.
An unfinished quoted reference remains text until completed.

While typing `@`, the TUI suggests files and directories. Bare `@` lists the
workspace's direct children, with directories first; a directory followed by `/`
lists its direct children. Nonempty queries use case-insensitive fuzzy matching
(ordered subsequences), including partial directory names such as `itnl/cfg`.
Relative queries match the whole workspace path across directory levels:
`cmd/m` finds both `cmd/aice/main.go` and `evals/go-service/reference/cmd/server/main.go`,
even though `cmd/` is an existing directory. A trailing `/` on an existing
directory browses only its direct children. Explicit `./`, `../`, `~/`, and
absolute parent paths anchor the search there; a nonempty suffix also searches
their descendants.
Use Up/Down to scroll through candidates, Right to insert the selected path and
keep matching (directories continue at the next level), Tab or Enter to confirm
a file or directory reference, and Escape to close. Confirmation closes the menu
without sending the draft; a subsequent Enter sends it. Confirmed references
display as unquoted `@path` attachments: the `@` is gray and the entire
path uses the theme's gold, including wrapped lines. Left/Right cross a confirmed
attachment as one unit; Backspace/Delete remove it as one unit, like an image
placeholder. Only confirmed occurrences are atomic; manually typed references
and paths inserted with Right remain editable character by character and keep
matching. Right also inserts paths without surrounding quotes, including names
with spaces; typing a separator after the expanded path ends that query.
Paths with spaces or quotes retain their exact spelling in reference
state and use escaped quotes when serialized for submission, history or the
external editor. Rejected submissions and deliveries restore the attachment
state; recalling history or returning from the external editor yields editable
text. Surrounding draft text is preserved. While a query is being edited, the
existing menu stays visible
until the latest search finishes, avoiding repeated transcript resizing during
the debounce interval. Tab, Enter and Right wait for fresh results, including
the initial search before the menu appears; Escape still closes the menu.
Search is debounced, cancellable, limited to two seconds and 20,000
entries, and keeps up to 1,000 permitted candidates in a scrolling menu. The
menu shows the selected position and candidate count. It skips `.git`, `.aice`,
`node_modules`, and `vendor`; it does not interpret `.gitignore`. An exact
reference can still name files outside the search results. Completion never
opens an approval prompt; explicit submission uses the normal Guard.

At most eight file references are allowed per submission. Each file uses the
same reader as the `read` tool: text contributes up to 2000 lines / 50 KiB with
a continuation notice; PNG/JPEG/GIF/WebP/BMP contributes image content;
directories contribute a shallow listing. Unsupported binary files fail explicitly. Image count and
byte limits include both file references and pasted images. Repeated paths are
read once. Contents are frozen before acceptance, including steering and queued
follow-ups; editing a source afterwards does not change accepted input.

File reads use the existing Guard, checking the requested name and resolved
physical target. Interactive approvals are cancellable; `--print` fails closed
on an Ask unless `--yolo`, which still cannot bypass a Deny. A failed attachment
rejects the complete submission and restores the interactive draft. `/btw`
remains tool-free and does not accept file or image attachments.

Dragged file paths and separate print-mode image flags are not implemented;
`--print 'Describe @image.png'` uses the same attachment pipeline.

## Operational environment variables

| Variable | Effect |
| --- | --- |
| `AICE_NO_DEP_INSTALL=1` | Disable ripgrep, Windows Git Bash and agent-browser downloads |
| `AGENT_BROWSER_EXECUTABLE_PATH` | Use an installed browser executable; see [Browser automation](browser.md) |
| `AICE_NO_UPDATE_CHECK=1` | Disable the interactive update check |

The two `AICE_NO_*` switches also support `true` and can be set to `false` or
`0` to override a lower-layer disable setting. They resolve once through the
configuration snapshot; consumers do not reread these environment variables.
`AGENT_BROWSER_EXECUTABLE_PATH` is a helper-owned executable input rather than
an AICE preference.

Installation, helper provisioning, and self-update details live in
[Installation and updates](installation.md).
