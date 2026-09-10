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
`deepseek-flash`. The `opencode-go` catalog default remains
`deepseek-v4-flash`.

The DeepSeek API catalog contains only `deepseek-flash` and `deepseek-v4-pro`.
Both accept text/image input. Flash retains Responses; Pro retains Anthropic
Messages. The OpenCode Go catalog is independent and unchanged.
Existing DeepSeek settings using removed model IDs must select one of these two
models; old IDs are not remapped.
DeepSeek cost estimates use official [off-peak rates](https://api-docs.deepseek.com/quick_start/pricing/);
peak billing is twice the estimate.

| Setting | Environment variable | Supported values |
| --- | --- | --- |
| Provider | `AICE_PROVIDER` | `deepseek`, `opencode-go`, `kimi-coding`, `moonshot`, `zhipu`, `zhipu-coding`, `openai`, `openai-codex`, `custom` |
| Model | `AICE_MODEL` | A catalog model, or any model ID for `custom` |
| Thinking | `AICE_THINKING` | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |
| Default Project Trust | none | `ask`, `always`, `never` |
| Custom base URL | `AICE_CUSTOM_BASE_URL` | OpenAI-compatible endpoint persisted as `custom_base_url`; default `http://localhost:11434/v1` |

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

The main TUI status bar shows only the used context percentage, such as
`82.40%`, with two decimal places. A new, untouched conversation shows `0.00%`;
a full window shows `100.00%`. After the first input is accepted, it uses the
latest successful response usage for the selected provider/model, including
cached input and output, plus estimated messages accepted since that response.
It does not divide cumulative Session usage by the window. Before the first
response and after compaction or switching models, AICE estimates the active
prompt, tool definitions, and projected history. Estimates use the same compact
percentage format. Unsent drafts and queued inputs are excluded until accepted.
Usage is clamped to 0–100%; at 70% or more it turns amber, and at 90% or more red.
Narrow terminals drop other details before the percentage.

Without configuration, the denominator uses the selected **provider and model**
catalog default, including when its base URL is overridden. An explicit
`context_windows` entry in `~/.aice/settings.json` takes precedence:

```json
{
  "context_windows": {
    "openai-codex/gpt-5.6-terra": 272000,
    "custom/Org/Model.v1": 32768
  }
}
```

These are configuration examples, not account entitlement claims. Keys match
exact `provider/model` IDs, including case and any slashes in the model ID;
values must be positive integer token counts. Overrides apply at startup and
survive `/model`, `/provider`, and `/login` changes. `/settings` shows the exact
window and its source. Restart after editing the file. The same resolved window
controls request protection and automatic compaction, including summary calls.

Set the limit to the context tier actually enabled on your endpoint/account.
A model's advertised maximum (including 1M) does not prove that every subscription
or gateway enables it. AICE does not probe account entitlements or enable remote
long-context tiers by changing this number; required server settings or protocol
opt-ins must already be supported and enabled. Catalog defaults describe the
built-in provider route. Codex subscription defaults to 272,000 tokens for
Astra, Sol, Terra, and Luna, matching OpenAI's
[official Codex catalog](https://github.com/openai/codex/blob/main/codex-rs/models-manager/models.json)
(`context_window`, checked 2026-09-07). Its 872,000 `max_context_window` is not
the default. The separately billed OpenAI API uses 1,050,000 for these models,
as documented on the [API model pages](https://developers.openai.com/api/docs/models).
DeepSeek V4 uses the documented
[1M default](https://api-docs.deepseek.com/quick_start/pricing).

Arbitrary `custom` IDs have no universal official default: for example,
[Ollama defaults depend on VRAM](https://docs.ollama.com/context-length).
AICE uses its existing 128,000-token fallback for these IDs and labels it
`custom fallback default` in `/settings`; this is not an official model limit.
The footer shows a percentage using that fallback so configuration is optional.
Override it when the deployment's actual limit is known. Check overrides again
when changing an endpoint or account. AICE does not read another harness's settings.

The separation of Session totals and context occupancy follows
[pi's footer](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/interactive/components/footer.ts)
and [context calculation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/agent-session.ts).
AICE uses its existing estimator after compaction.

### Thinking levels

`AICE_THINKING` uses seven canonical levels. Each model supports a subset,
declared by its provider catalog. AICE aligns an unsupported request to the
nearest supported level, preferring the next higher one and then the next
lower one. The effective level therefore always belongs to the selected
model's subset. The requested level remains saved so switching models can
restore it; `/settings` shows the effective level and `/thinking` lists only
valid choices for the active model. Models without thinking support expose
only `off`.

The default request is `medium`. On DeepSeek Flash and V4 Pro it becomes
`high`; on OpenCode Go Kimi K3 it becomes `max`. Important built-in subsets are:

| Provider and model | Supported levels |
| --- | --- |
| `deepseek/deepseek-flash`, `deepseek/deepseek-v4-pro` | `off`, `low`, `high`, `max` |
| `opencode-go/deepseek-v4-flash` | `low`, `high`, `max` |
| `opencode-go/deepseek-v4-pro` | `high`, `max` |
| `opencode-go/deepseek-v4-flash-vision-exp` | `off`, `low`, `high`, `max` |
| `opencode-go/kimi-k2.6` | `off`, `high` |
| `opencode-go/kimi-k3` | `max` |
| `zhipu/glm-5.3`, `zhipu/glm-5.3-flash`, `zhipu-coding/glm-5.3`, `zhipu-coding/glm-5.3-flash` | `low`, `high`, `max` (thinking enabled) |
| `moonshot/kimi-k3` | `low`, `high`, `max` |
| `moonshot/kimi-k2.7-code`, `moonshot/kimi-k2.7-code-highspeed` | `high` (thinking enabled) |
| `moonshot/kimi-k2.6` | `off`, `high` |
| `kimi-coding/k3`, `kimi-coding/k3-256k` | `low`, `high`, `max` |
| `kimi-coding/kimi-for-coding`, `kimi-coding/kimi-for-coding-highspeed` | `high` (thinking enabled) |
| `opencode-go/glm-5.2` | `high`, `max` |
| `opencode-go/glm-5.3`, `opencode-go/glm-5.3-flash` | `low`, `high`, `max` |
| `opencode-go/gpt-5.6-luna` | `off`, `low`, `medium`, `high`, `xhigh`, `max` |
| `opencode-go/grok-4.6` | `low`, `medium`, `high`, `xhigh` |
| `opencode-go/muse-spark-1.2-contributor`, `opencode-go/muse-spark-1.3-contributor` | `minimal`, `low`, `medium`, `high`, `xhigh` |
| `opencode-go/omen-alpha` | `low`, `high` |
| `opencode-go/hy3` | `off`, `low`, `high` |
| `opencode-go/hy4-preview` | `off`, `high` |
| `openai/gpt-6-astra` | `low`, `medium`, `high`, `xhigh`, `max` |
| `openai/gpt-5.6*` | `off`, `low`, `medium`, `high`, `xhigh`, `max` |
| `openai-codex/gpt-6-astra`, `openai-codex/gpt-5.6-{sol,terra,luna}` | `low`, `medium`, `high`, `xhigh`, `max` |
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

The built-in OpenAI catalog contains `gpt-6-astra`, `gpt-5.6-sol`,
`gpt-5.6` (the Sol alias), `gpt-5.6-terra`, and `gpt-5.6-luna`.
`gpt-5.6-terra` remains the default. All use the official Responses API;
GPT-5.6 supports `off`, `low`, `medium`, `high`, `xhigh`, and `max`, while
Astra supports `low` through `max` and cannot disable reasoning.

Metadata was checked on 2026-09-07 against the official
[Astra](https://developers.openai.com/api/docs/models/gpt-6-astra),
[Sol](https://developers.openai.com/api/docs/models/gpt-5.6-sol), and
[Luna](https://developers.openai.com/api/docs/models/gpt-5.6-luna) model pages.
Standard input/output estimates per million tokens are $10/$50 for Astra,
$4/$20 for Sol and its alias, $2/$12 for Terra, and $0.20/$1.20 for Luna.
AICE's flat pricing metadata does not model long-context or service-tier
surcharges; displayed costs are estimates, not billing totals.

The OpenCode Go catalog contains the 27 active upstream models; entries marked
deprecated upstream are omitted. The catalog was checked on 2026-09-07 against
the [gateway model list](https://opencode.ai/zen/go/v1/models) and
[models.dev metadata](https://models.dev/api.json). Omen Alpha uses Chat
Completions with text/image input and low/high reasoning. GPT-5.6 Luna, Grok 4.6, and Muse Spark
1.2/1.3 Contributor use the Responses protocol. Qwen3.6 through Qwen3.8
(including Qwen3.8 Flash) and MiniMax M2.7/M3 use Anthropic Messages; the
remaining catalog uses Chat Completions.
Models whose upstream input modalities include images accept image content
through the LLM contract and [clipboard image input](#clipboard-images).

### Client identity and subscription use

All model requests through AICE's three protocol adapters identify the client
as `User-Agent: aice/<version>`, including custom endpoints. The version is the
same build value shown by `aice --version`; unstamped source builds send
`aice/dev`. The release workflow stamps the release version. This header does
not include an account ID, hostname, workspace path, or prompt. There is no
user-facing client-identity override. Provider-specific headers cannot replace
the Responses adapter's AICE identity.

Use the built-in `kimi-coding` and `opencode-go` providers for these subscription
services. Merely pointing `custom` at OpenCode Go does not enable its dedicated
Session routing transport. When forwarding through a proxy, preserve the AICE
User-Agent and conversation header on every request, including retries and
compaction; verify headers at the upstream side without logging API keys or
prompt bodies.

The [Kimi documentation](https://www.kimi.com/code/docs/) requires truthful
client identity and distinguishes subscription coding use from product
integration through its open platform. The
[OpenCode Go documentation](https://opencode.ai/docs/go/#where-can-i-use-it)
requires typical coding-agent traffic, a specific client identity, and stable
conversation routing. These headers describe the client; they are neither an
authentication mechanism nor a guarantee of provider approval.

For personal coding, use your own authorized credentials and remain within the
provider's current plan limits. For shared services, product backends, or large
batch evaluations, confirm that the intended workload is permitted or choose
an API plan intended for that use. Do not rotate accounts or identities to
bypass limits. Check the provider's current terms rather than assuming a fixed
request count is always safe.

AICE's model-call retry policy allows three retries with exponential backoff,
respects longer provider retry hints, and stops when a hint exceeds its maximum
wait. SDK retries are disabled. This is per-call protection, not an
account-wide rate limiter: multiple processes and concurrent side conversations
can still add load. Avoid external scripts that immediately restart exhausted
requests. See [model retries](contracts.md#agent-loop) for runtime ownership.

OpenCode Go requests across all three protocols carry `x-opencode-session`
and identify the client as `aice/<version>`, as required by the
[Go gateway](https://opencode.ai/docs/go/#where-can-i-use-it).
For Chat Completions and Responses, requests without an explicit output limit
omit the output-token parameter, allowing OpenCode to select its default.
An explicit limit is still sent; model token limits remain available for local
context budgeting.

The application propagates the stored Session ID as routing metadata through
the request context, preserving it across turns, retries, model changes,
compaction, and reopening. `/new` creates a new identity with the next Session.
Stateless print runs and individual `/btw` threads have their own ephemeral
identities. Direct provider callers can supply `llm.WithSessionID`; without it,
the provider uses a random identity stable for that provider instance.

OpenCode Go Chat Completions requests omit `max_tokens` when AICE has no
explicit output-token cap, allowing the gateway to choose its current default.
An explicit `MaxTokens` value, including one produced by context protection,
is still sent. Responses models use that protocol's normal output-token field.
Other providers keep sending their model default.

For arbitrary `custom` models without a context override, AICE uses a
128,000-token fallback budget and 16,384-token output limit, text/image input, and
standard thinking levels. The footer uses this budget until overridden;
`/settings` identifies it as a fallback.
These are fixed metadata defaults from `custom.ModelForID`, not capabilities
queried from the endpoint. A server with smaller limits or different reasoning
support can reject a request despite local budget checks. Custom permits image
attachments and image results from `read` by default; the endpoint decides
whether it supports them. Unsupported input or parameters surface as server
errors; AICE does not automatically strip images or parameters and retry.
Automatic capability detection is not implemented, and absent custom pricing
is not evidence that a request is free.

`default_project_trust` defaults to `ask`. Automation should use `--approve`
or `--no-approve` rather than a broad environment override. See [Project Trust
and prompts](project-trust.md) for protected resources and decision order.

## Credentials and connection overrides

API keys are stored by provider in `~/.aice/auth.json` with file mode
`0600`. A process environment variable overrides the stored key. Codex OAuth
credentials use a separate file as described below.

| Provider | API key environment variable | Auth file key | Base URL override |
| --- | --- | --- | --- |
| DeepSeek | `AICE_DEEPSEEK_API_KEY` | `deepseek_api_key` | `AICE_DEEPSEEK_BASE_URL` |
| OpenCode Go | `AICE_OPENCODE_API_KEY` | `opencode_api_key` | `AICE_OPENCODE_BASE_URL` |
| Kimi Coding Plan | `KIMI_API_KEY` | `kimi_api_key` | `AICE_KIMI_BASE_URL` |
| Moonshot API (China) | `MOONSHOT_API_KEY` | `moonshot_api_key` | `AICE_MOONSHOT_BASE_URL` |
| Zhipu Coding Plan | `ZHIPU_CODING_API_KEY` | `zhipu_coding_api_key` | `AICE_ZHIPU_CODING_BASE_URL` |
| Zhipu API (China) | `ZHIPU_API_KEY` | `zhipu_api_key` | `AICE_ZHIPU_BASE_URL` |
| OpenAI | `OPENAI_API_KEY` | `openai_api_key` | `AICE_OPENAI_BASE_URL` |
| Custom (Ollama, vLLM, LM Studio, any OpenAI-compatible) | `AICE_CUSTOM_API_KEY` | `custom_api_key` | `AICE_CUSTOM_BASE_URL` (default `http://localhost:11434/v1`) |

In the TUI, `/login` first offers `Sign in with an account` or
`Sign in with an API key`, then a provider menu. For an API-key provider whose credential is
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

### Kimi Coding Plan

Select `/login` → `Sign in with an API key` → `Kimi Coding Plan`, using a key
from the Kimi Code console. The provider ID is `kimi-coding`; it connects
directly to `https://api.kimi.com/coding/v1/responses` using the shared OpenAI
Responses adapter, with streaming text, reasoning replay, and function calls.
The client identifies itself as `aice`. This uses a Coding Plan key, separate
from Moonshot's pay-as-you-go API credentials.

For environment-based setup:

```sh
export KIMI_API_KEY="your-coding-plan-key"
export AICE_PROVIDER=kimi-coding
export AICE_MODEL=kimi-for-coding
aice
```

To store the key instead, run `printf '%s\n' "$KIMI_API_KEY" | aice config set-key --provider kimi-coding`.
This saves only the credential; select the provider through `AICE_PROVIDER`,
global settings, or `/provider`.

The catalog contains `kimi-for-coding` (default, all members),
`kimi-for-coding-highspeed`, `k3-256k`, and `k3`. Availability depends on the
membership tier. All accept text and [clipboard images](#clipboard-images). K3 offers `low`, `high`, and `max`; K2.7 Code keeps
thinking enabled with `high`. Unsupported levels are clamped as usual, so
`off` does not silently route these model IDs to K2.6.

All four use a conservative 262,144-token context default and a 32,768-token
AICE output budget (not a claim about the server's maximum output). If your
membership enables K3's 1M tier, set `"kimi-coding/k3": 1048576` under
`context_windows` in global settings. Token usage is recorded with zero
per-token price estimates; subscription quotas still apply.

Protocol and model capabilities were checked on 2026-09-07 against Kimi's
[Responses integration guide](https://www.kimi.com/code/docs/en/third-party-tools/codex.html)
and [model configuration](https://www.kimi.com/code/docs/en/kimi-code/models.html).

### Moonshot API Platform

Select `/login` → `Sign in with an API key` → `Moonshot API`. Enter the
API key from the China platform at [platform.kimi.com](https://platform.kimi.com).
The `moonshot` provider includes the official `https://api.moonshot.cn/v1`
endpoint; no URL configuration is needed. Its credentials are separate from
`kimi-coding`: platform requests use prepaid, per-token API billing rather
than Coding Plan quota. `KIMI_API_KEY` never supplies a Moonshot credential.

For environment-based setup:

```sh
export MOONSHOT_API_KEY="your-platform-key"
export AICE_PROVIDER=moonshot
export AICE_MODEL=kimi-k3
aice
```

Alternatively, store the key with `printf '%s\n' "$MOONSHOT_API_KEY" | aice config set-key --provider moonshot`.
As with other providers, this command stores only the key; `/login` also
selects and saves the provider and a compatible model.

| Model | Protocol | Context / default output budget | Thinking |
| --- | --- | --- | --- |
| `kimi-k3` (default) | Responses | 1,048,576 / 131,072 | `low`, `high`, `max` |
| `kimi-k2.7-code` | Responses | 262,144 / 32,768 | Always enabled |
| `kimi-k2.7-code-highspeed` | Responses | 262,144 / 32,768 | Always enabled |
| `kimi-k2.6` | Responses | 262,144 / 32,768 | `off`, `high` |

All catalog models use the shared Responses adapter, as does Kimi Coding Plan.
Thinking uses `reasoning.effort`; K2.6 `off` maps to `none`. There is no
model-dependent protocol selection or Chat Completions fallback.
AICE's default requested `medium` becomes `high` on these models. Text/image
inputs, streamed reasoning, and tool-result replay use the existing adapters.
The composer supports [clipboard images](#clipboard-images). Retired K2.5 and
Moonshot V1 models are not included.

This preset targets the China platform. `AICE_MOONSHOT_BASE_URL` is an optional
advanced override, not a region auto-detection mechanism; keys and model
availability must match the destination platform. AICE records token usage,
but does not convert China-platform CNY prices into its USD cost estimates;
a zero displayed estimate does not mean the API call is free. Use the platform
billing console for charges.

Official references for the catalog and Responses request format:
[model list](https://platform.kimi.com/docs/models),
[Responses reference](https://platform.kimi.com/docs/api/responses), and
[parameter reference](https://platform.kimi.com/docs/api/models-overview).

### Zhipu API Platform

Select `/login` → `Sign in with an API key` → `Zhipu API`, or configure:

```sh
export ZHIPU_API_KEY="your-platform-key"
export AICE_PROVIDER=zhipu
export AICE_MODEL=glm-5.3
aice
```

The China BigModel endpoint is built in:
`https://open.bigmodel.cn/api/paas/v4/chat/completions`.
To store only the key, use `printf '%s\n' "$ZHIPU_API_KEY" | aice config set-key --provider zhipu`.
`/login` also saves the provider and compatible model. The optional
`AICE_ZHIPU_BASE_URL` overrides the API root (without `/chat/completions`).

The API Platform catalog includes the following tool-capable chat models.
GLM-5.3 remains the default; select another model through `/model` or
`AICE_MODEL`. Availability still depends on the platform account.

| Model IDs | Input | Context / AICE output budget | Thinking choices |
| --- | --- | --- | --- |
| `glm-5.3` | Text | 1,000,000 / 131,072 | `low`, `high`, `max` |
| `glm-5.3-flash` | Text, image | 1,000,000 / 131,072 | `low`, `high`, `max` |
| `glm-5.2` | Text | 1,000,000 / 131,072 | `off`, `high`, `max` |
| `glm-5.1`, `glm-5`, `glm-5-turbo` | Text | 200,000 / 131,072 | `off`, `high` |
| `glm-4.7`, `glm-4.7-flashx`, `glm-4.7-flash`, `glm-4.6` | Text | 200,000 / 131,072 | `off`, `high` |
| `glm-4.5-air`, `glm-4.5-airx` | Text | 128,000 / 98,304 | `off`, `high` |
| `glm-4-flashx-250414`, `glm-4-flash-250414` | Text | 128,000 / 16,384 | `off` (no thinking control) |
| `glm-5v-turbo` | Text, image | 200,000 / 131,072 | `off`, `high` |
| `glm-4.6v`, `glm-4.6v-flashx`, `glm-4.6v-flash` | Text, image | 128,000 / 32,768 | `off`, `high` |

For GLM-5.3 and Flash, thinking is always enabled. The default request
`medium` clamps to `high`, while `off` clamps to `low`. GLM-5.2 additionally
supports disabling thinking; equivalent upstream effort aliases are collapsed
into the distinct `high` and `max` choices. Only GLM-5.2 and newer send
`reasoning_effort`. Older reasoning models send only `thinking.type`;
`high` means enabled without a separate effort parameter. Non-thinking GLM-4
Flash models omit both controls.

Streaming text, reasoning, function calls, usage and same-model
`reasoning_content` replay use the shared Chat Completions adapter.
Preserved-thinking defaults remain controlled by the endpoint. The LLM
boundary and [clipboard input](#clipboard-images) accept images for the listed
visual models. AICE does not implement video or file input blocks.

Context defaults conservatively interpret the overview's 200K and 128K as
200,000 and 128,000 tokens. Output budgets follow the explicit parameter
limits. For `glm-4-flash-250414`, the model overview says 16K while the
parameter table permits 32,768; AICE keeps the lower 16,384 budget. Image,
video, speech, embedding, OCR and other specialized output APIs are outside
this chat provider. Retired `glm-4.5-flash` (redirected to `glm-4.7-flash`)
and deprecated GLM-4.5/4.5-X are omitted.

This is the separately billed API platform. Token counts are recorded, but
CNY prices are not converted into AICE's USD estimates; a zero estimate does
not mean free usage. The preset retains AICE's own client identity.

Catalog facts were checked on 2026-09-07 against the official
[model overview](https://docs.bigmodel.cn/cn/guide/start/model-overview),
[parameter limits](https://docs.bigmodel.cn/cn/guide/start/concept-param),
[GLM-5.3](https://docs.bigmodel.cn/cn/guide/models/text/glm-5.3),
[GLM-5.3-Flash](https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5.3-flash),
[GLM-4.5 retirement notice](https://docs.bigmodel.cn/cn/guide/models/text/glm-4.5),
[GLM-4.5-Flash retirement notice](https://docs.bigmodel.cn/cn/guide/models/free/glm-4.5-flash),
and [thinking/replay guide](https://docs.bigmodel.cn/cn/guide/capabilities/thinking-mode).
The model pages linked from the overview own individual input and tool capabilities.

### Zhipu Coding Plan

Select `/login` → `Sign in with an API key` → `Zhipu Coding Plan`, or configure:

```sh
export ZHIPU_CODING_API_KEY="your-coding-plan-key"
export AICE_PROVIDER=zhipu-coding
export AICE_MODEL=glm-5.3
aice
```

The preset uses `https://open.bigmodel.cn/api/coding/paas/v4/chat/completions`.
Its API root override is `AICE_ZHIPU_CODING_BASE_URL`. Store only the credential
with `printf '%s\n' "$ZHIPU_CODING_API_KEY" | aice config set-key --provider zhipu-coding`.
The Coding Plan key and endpoint are independent of `zhipu`: neither provider
falls back to the other's credentials or URL, including when quota is exhausted.
Team subscriptions require the key from the team plan console.

The Coding Plan catalog contains `glm-5.3` (default) and `glm-5.3-flash`,
with the same model capabilities documented above. Both are available across
plan tiers according to the official overview. The plan redirects `glm-5.2`
and `glm-5.1` to `glm-5.3`, and `glm-5-turbo` and `glm-4.7` to
`glm-5.3-flash`; AICE lists the actual targets rather than these historical
aliases. Other API Platform models are not accepted by the Coding Plan preset.
Token usage is recorded with zero per-token estimates; subscription quotas
still apply. The endpoint controls preserved-thinking defaults, while AICE
replays same-provider/model reasoning and tool results through the shared adapter.

The official [quick start](https://docs.bigmodel.cn/cn/coding-plan/quick-start)
and [plan overview](https://docs.bigmodel.cn/cn/coding-plan/overview) restrict
plan quota to supported tools and product environments. AICE retains its own
client identity; this compatibility preset does not assert that AICE has been
approved by Zhipu for subscription quota. Actual access depends on the account
and platform eligibility and has not been verified with a live credential.

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
transient filesystem contention. If waiting
fails, the error preserves both the cancellation/deadline and the last lock
error so persistent permission failures remain visible. After a crash, remove
that stale lock directory only when no AICE process is running. Failed refresh
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

Restart AICE after installing or removing skills. `/new` resets Session
history but reuses the startup skill catalog, tools, and prompt; it does not
rescan skills. The `/skills` reminder reports that restart requirement.

## Interactive commands

The startup screen centers the animated AICE logo above a compact welcome
card, with a blank row between them. The card shows the next action, command
or login hints, and version/update status; it does not list tools. Narrow or
short terminals show the card alone when the logo does not fit.

| Command | Effect |
| --- | --- |
| `/help` | List commands |
| `/btw [question]` | Create or choose an ephemeral, tool-free side thread |
| `/init` | Create or improve root `AGENTS.md`; loaded after restart |
| `/settings` | Show effective model, Trust state, and configuration paths |
| `/skills` | List Agent Skills loaded for this Session |
| `/login` | Choose account or API key, then provider; Codex offers browser/device login; `custom` uses endpoint → key (may be empty) → model |
| `/provider` | Select and save the global provider |
| `/model` | Select and save a model from that provider |
| `/thinking` | Select and save a supported reasoning level |
| `/trust` | Save a Trust choice for restart; temporary choices are available only at startup |
| `/session` | Show the Session ID, path, active leaf, and counts |
| `/tree` | Show all Session branches |
| `/checkout` | Select where the next branch starts |
| `/compact` | Append a compaction checkpoint for the active branch |
| `/new` | Detach from the current Session; the next prompt starts fresh |
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
exits. In a side panel, Enter asks a follow-up, Escape cancels the visible side
answer (or closes the panel when idle), and Alt+Escape returns to the main
view without cancelling. Ctrl+C clears the visible editor; a consecutive
second press exits AICE. Ctrl+D ends the thread; ending a running thread asks
for confirmation and waits for its answer to stop before deleting it.

## Interactive input delivery

`Ctrl+O` expands or collapses main-task process details. While an assistant
response is streaming, long thinking shows only its most recent 4 KiB with an
earlier-content notice, keeping the terminal responsive. This also applies to
BTW thinking. When the response ends, expanded views show the complete thinking;
the preview does not remove content from the conversation or main Session.

Write tool rows show a content preview of at most 10 source lines / 4 KiB by
default. Long lines are clipped to the terminal width. Use the existing
`Ctrl+O` process toggle to collapse, then expand for a larger preview (up to
2000 lines / 64 KiB). A completed, automatically collapsed process needs only
one press to expand. Preview limits are labelled; they do not limit file writes.
During argument streaming, the row says `preview · not executed`; incomplete
JSON escapes wait for more input. A stream preview retains only the first
64 KiB of raw arguments, so later content or a late path may appear only when
the complete call arrives. Source control characters are replaced for display.

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
empty. A consecutive second `Ctrl+C` on the empty editor exits; another key or
text paste resets the sequence. Clearing a draft does not remove queued inputs
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

While typing `@`, the TUI suggests paths and fuzzy filename matches. Use Up/Down
to select, Tab to complete, and Escape to close; Enter sends the draft. Selecting
a directory continues completion inside it. Spaces and quotes are escaped for
submission. While a query is being edited, the existing menu stays visible
until the latest search finishes, avoiding repeated transcript resizing during
the debounce interval. Tab waits for fresh results; Escape still closes the menu.
Search is debounced, cancellable, limited to two seconds and 20,000
entries, and shows at most eight permitted candidates. It skips `.git`, `.aice`,
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
| `AICE_NO_DEP_INSTALL=1` | Do not download missing ripgrep or Windows Git Bash helpers |
| `AICE_NO_UPDATE_CHECK=1` | Disable the interactive update check |

Installation, helper provisioning, and self-update details live in
[Installation and updates](installation.md).
