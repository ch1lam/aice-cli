# Web search and fetch

AICE searches the web in two ways. Provider-hosted (native) search lets a
model that supports it search on the provider's servers during its response;
see [native search](#native-search). The built-in `web_search` tool sends a
query to a configured independent search service, and `web_fetch` retrieves one
page through HTTP. The two built-in tools are ordinary tools: they pass through
the execution gate, produce a paired tool result, and record their sources in
Session history.

## Search sources and priority

`web.search.priority` is an ordered list of sources. Two entry kinds exist:

- `native` — provider-hosted search for models that support it. It is always
  usable; whether a request's model supports it is decided per request.
- `service:<id>` — an instance configured under `web.services`.

At the start of each run AICE resolves the first usable entry. When that is
`native`, the first usable service after it is the fallback: models without
hosted search get `web_search` through it. The resolver is static: it checks
configuration, enabled state and credential presence, never network
reachability. Rules:

| Configuration | Outcome |
| --- | --- |
| Default (no `web` object) | `priority` is `["native"]`; models with hosted search use it, other models get no search tool |
| `["native", "service:exa-main"]`, Exa ready | Hosted search where the model supports it, Exa's `web_search` otherwise |
| `["service:exa-main", "native"]` | Exa is used for every model; hosted search is never enabled |
| `[]` | No search source is allowed; `web_search` is not registered |
| Invalid file schema or priority referencing an unknown instance | Configuration loading fails with the offending key |
| Resolver reaches an unknown provider/API or invalid provider options | Search is unavailable; no later service is chosen |
| First usable entry lacks its credential | `missing_credentials` is reported; AICE does not fall back to another account |
| Instance `enabled: false` | Skipped with `disabled`, even when its definition is invalid; later entries are considered |
| A search request fails (401, 429, timeout…) | The tool returns a classified error; no second backend is tried |
| A search returns no results | A successful empty result |

When no source is usable the `web_search` schema is not sent to the model at
all; `/web` and `/settings` show the reason. A model never sees both kinds at
once: a request that enables hosted search omits the local `web_search`. `web_fetch` is independent of any
search credential and is registered whenever `web.fetch.enabled` is true.

## Configuration

Web configuration lives in the `web` object of `~/.aice/settings.json`. Keys
are validated strictly; unknown fields are errors.

```json
{
  "web": {
    "search": {
      "enabled": true,
      "priority": ["native", "service:exa-main"],
      "default_max_results": 8,
      "timeout": "25s",
      "allowed_domains": [],
      "excluded_domains": []
    },
    "services": {
      "exa-main": {
        "provider": "exa",
        "api": "exa-rest",
        "base_url": "https://api.exa.ai",
        "credential": { "env": "EXA_API_KEY" },
        "options": { "type": "auto" }
      }
    },
    "fetch": {
      "enabled": true,
      "timeout": "30s"
    }
  }
}
```

- `provider` is `exa` or `deepseek`. `api` and `base_url` may be omitted; the
  descriptor supplies `exa-rest` and `https://api.exa.ai` for Exa, or
  `deepseek-search` and `https://api.deepseek.com` for DeepSeek. `base_url`
  accepts an HTTPS URL with an optional path prefix; requests append `/search`
  (Exa) or `/v1/tools/search` (DeepSeek). Userinfo, query and fragment
  are rejected. HTTP is accepted only for loopback development gateways:
  `localhost` or a loopback IP literal such as `127.0.0.1` or `[::1]`.
- `credential` is exactly one of `{"env": "NAME"}` or
  `{"auth_ref": "web_services.<id>"}`. Keys are never stored in
  `settings.json`. `auth_ref` reads the `web_services` object in
  `~/.aice/auth.json`, which `/web` writes when you enter a key; a
  `web_services` object in any `settings.json` is ignored with a startup
  diagnostic.
- Instance IDs match `[a-z0-9][a-z0-9_-]{0,63}`. The same provider can appear
  several times with different IDs and endpoints. Each instance has its own
  credential reference; environment references may name the same variable.
- Exa `options.type` accepts `auto` (default) and `fast`. Deep or synthesized
  modes are rejected explicitly rather than mapped to `auto`. DeepSeek accepts
  no options; any key is an error.
- `default_max_results` is 1–20 (default 8); the model may request 1–20 per
  call. `timeout` values are AICE product defaults, not upstream guarantees.
- `allowed_domains` / `excluded_domains` are bare hostnames and act as the
  upper bound: a model request can only narrow the allow list (label-boundary
  subdomain matching); the exclude list comes only from configuration. Both
  lists may be set together, in `settings.json` or `/web`. An
  empty intersection is a `constraint_conflict` error and nothing is sent
  upstream.
- `enabled: false` under `search` or `fetch` disables that tool. A missing
  `priority` uses the default; an explicit `[]` allows no source.
- Setting `EXA_API_KEY` or `DEEPSEEK_API_KEY` in the environment does nothing
  by itself. An instance must be added by the user before any request can be
  sent; a DeepSeek search instance has its own credential reference even when
  the same key also serves the DeepSeek model provider.

Only user-global settings and interactive changes define services, endpoints,
credentials and priority. A trusted project's `.aice/settings.json` may
contain only `web.search.enabled=false` and/or `web.fetch.enabled=false`; any
other `web` content or a `web_services` object in a project file is ignored
with a startup diagnostic. `priority` is replaced as a whole array, and each
service instance is an indivisible unit; layers are never merged field by field.

Saves patch only the touched fields under the settings lock. If a credential
save succeeds but a later preference save fails, AICE reports partial completion;
the current backend keeps its previous credential until the next Web publication.
Removing a service saves preferences first, so a later credential deletion failure
can leave an unused key in the auth store.

## The `/web` command

`/web` opens a menu that can show status, add an Exa or DeepSeek instance (key
entered hidden and saved to the auth store, or a reference to `EXA_API_KEY` or
`DEEPSEEK_API_KEY`), replace
an instance credential, remove an instance and its stored credential, move a
priority entry up or down, remove or append entries (including `native`), and
toggle search or fetch. New instances are appended to the end of the priority
list so an existing order is never overtaken. Instance IDs are assigned
automatically (`exa-main`, `exa-2`, `deepseek-main`, …) and can be renamed in
the file.

Changes require idle main and BTW responses and apply to the next response.
Status remains available through `/web` during a run. Menus and edits never test
the search API or send paid requests.

Settings → Tools & Network shows the selected source on **Search sources**;
Enter shows the full status above the add, credential and remove instance
actions. Search priority is a list row reordered with Ctrl+Up/Down, and the
switches, result count, timeouts, domain lists and per-instance options are
ordinary rows. It shows user preferences and project restrictions separately. Successful publication replaces the backend,
tools, prompt and Guard binding together; prepared responses are invalidated.
The application closes superseded and exiting backends. See
[Settings](configuration.md#settings-window) and the
[Web lifecycle tests](../internal/app/web_lifecycle_test.go).

## Permissions

Enabled web tools access the network without a web-specific confirmation in
interactive and Print mode. Search requires an application-bound service; fetch
requires a valid URL. In auto mode (the default) the action classifier reviews
every `web_search` and `web_fetch` call and may block it; see
[auto mode](execution-sessions.md#auto-mode). Invalid targets still fail and `--yolo` never lifts a deny. Disabling
either tool unregisters it. Project Trust does not grant or revoke Web access;
`/btw`, compaction and `/init` are tool-free.

These checks apply to the Web tools only. Other tools such as Bash retain the
process's network access, so disabling Web tools does not take AICE offline.

## `web_fetch` limits

`web_fetch` accepts absolute `http`/`https` URLs on the default ports (80/443)
without userinfo or zone-scoped IPv6 literals. An explicit `localhost` name or
a blocked IP literal is refused before any request: loopback, private,
link-local, CGNAT, multicast, unspecified, NAT64, 6to4 and documentation
ranges, including the link-local metadata address. IPv4-mapped IPv6 is checked
after converting to IPv4. Hostnames that are not literals are left to the standard HTTP transport (and the proxy
side, when one applies) to resolve; AICE performs no DNS pre-resolution and
pins no addresses. A hostname resolving to a private address is therefore not
blocked by this policy. TLS verifies the URL hostname. Each redirect hop (at most
5) is revalidated under the same URL-shape and literal-target policy, with
no `https` to `http` downgrade. Legal cross-origin redirects continue
automatically; dangerous targets, downgrades and over-limit chains still
fail.

Requests use the standard HTTP transport and proxy environment
(`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`, including lowercase forms), with no
direct retry after proxy failure. The fetcher shares no cookies, headers or
credentials with model/search clients. Raw and decoded bodies are each limited
to 5 MiB; the whole fetch uses `web.fetch.timeout` (default 30 s).
Supported types are plain text, Markdown, HTML and XHTML. Missing/generic types
are sniffed; other types fail as `unsupported_content`. Declared charsets are
converted to UTF-8.

HTML is parsed into a DOM. Scripts, styles, frames, embeds, navigation, asides
and footers are removed; the `<main>`, `<article>` or `<body>` element is
selected and the method is reported. Headings, paragraphs, links (resolved
against the final URL, ignoring `<base>`), lists, code blocks, tables and quotes
are converted to Markdown or plain text. This is a basic extraction, not a full
readability engine. Extracted text retained as evidence is limited to 32 KiB
with an explicit truncation flag; the model-facing output is also bounded.

## Results and evidence

Search results and fetched pages are returned to the model as deterministic
text marked as external, untrusted content. Search output lists numbered
results with title, URL, optional published date and evidence excerpts, followed
by a reminder that excerpts are not the full page. Output never contains
timestamps, request IDs or durations, so identical results stay identical for
repeated-tool detection while changed results count as progress. Terminal
control sequences are stripped from all upstream text.

Structured evidence records source URLs, titles, supplied publication dates,
excerpts/documents, retrieval times and truncation in the same Session tool
result. URL-derived source IDs are stable. Diagnostics such as upstream request
IDs, timings and reported costs stay outside model text. Evidence is bounded to
64 KiB and survives Session replay; the TUI displays recorded sources below the
tool output. Exa highlights remain excerpts, separate from any returned
summaries; DeepSeek's model-oriented `summary` passage is an excerpt.

## Exa adapter

The Exa adapter (`provider: "exa"`, `api: "exa-rest"`) sends exactly one `POST /search` per tool call with `query`, `type`,
`numResults`, optional `includeDomains`/`excludeDomains`, and
`contents: {"text": false, "highlights": true}`; it requests no full text,
summaries or deep-search synthesis. The key travels in `x-api-key`; redirects
are never followed so the key cannot reach another origin. Success bodies are
limited to 2 MiB and error bodies to 8 KiB. HTTP status maps to
`authentication` (401/403), `quota_exceeded` (402), `rate_limited` (429,
retaining `Retry-After`), `invalid_argument` (400/422) and `unavailable` (5xx);
any other status is `invalid_response`.
Malformed JSON, a missing `results` field or an error envelope with status 200
are `invalid_response`; an empty array is a successful empty result. Paid
requests are never retried automatically. Reported `costDollars.total` is
recorded as an upstream cost in USD; a missing value stays unknown, never zero.

Default tests use local fixtures. A real request requires the `integration`
tag and explicit opt-in; see [Verification](collaboration.md#web-checks).
Live Exa, public-page fetch and native `/web` terminal acceptance are not
recorded.
Implementation: [binding](../internal/app/web.go),
[configuration](../internal/config/web.go),
[Exa](../internal/web/exa/client.go), and
[fetch](../internal/web/httpfetch/fetch.go).

## DeepSeek adapter

DeepSeek search (`provider: "deepseek"`, `api: "deepseek-search"`) sends
exactly one `POST /v1/tools/search` per tool call with `query` and `count`;
the key travels as a Bearer token and redirects are never followed. Domain
constraints become query operators: allowed domains join as
`site:a OR site:b` and exclusions add `-site:x`. The API limits the query to
128 characters including these operators, so a longer query fails as
`invalid_argument` before anything is sent. Each result's `summary` (the
passage DeepSeek extracts for a model) is the excerpt, falling back to the
display `snippet`; titles, URLs and `date` become sources. Status mapping
follows the Exa adapter, with 408 also `unavailable`. DeepSeek bills $0.75 per
1,000 successful requests (empty results and failures are free); the response
reports no cost, so the recorded cost stays unknown.

Default tests use local fixtures. The opt-in integration test is
`AICE_DEEPSEEK_SEARCH_INTEGRATION=1 DEEPSEEK_API_KEY=... go test -tags=integration ./internal/web/deepseek -run TestRealDeepSeekSearch`;
it passed against the live API on 2026-11-27.
Implementation: [DeepSeek](../internal/web/deepseek/client.go) and the shared
[service helpers](../internal/web/service.go).

## Native search

With `native` first, each request whose model declares hosted search and
accepts the run's policy enables it; otherwise the request keeps the fallback
service's `web_search`, if any. The decision is made for every request
([`BuildRequest`](../internal/llm/request_build.go)), so switching model or
thinking level mid-Session switches between the two. Subagents inherit the
policy and decide by their own model.

| Model | Tool | Conditions |
| --- | --- | --- |
| OpenAI (Responses) | `web_search` | Allowed domains become `filters.allowed_domains`; excluded domains fall back to the service |
| Claude API | `web_search_20250305` | Allowed or excluded domains, not both. An organization administrator can disable web search in the Claude Console; requests then fail until `priority` puts a service first |
| DeepSeek Flash (Responses) | `deepseek_web_search` | Thinking on (the API rejects it beside function tools without thinking); no domain lists |
| DeepSeek V4 Pro (Anthropic endpoint) | `web_search_20250305` | Thinking on or off; domain lists are unverified, so any domain policy falls back |

Other providers, the Codex and Claude subscriptions, and custom OpenAI-compatible
endpoints declare no hosted search. The global `allowed_domains` and
`excluded_domains` apply; a list the provider tool cannot enforce makes that
request fall back rather than search unrestricted. Per-call domain arguments
and `max_results` do not exist for hosted search.

Hosted search runs on the provider, outside AICE's [execution
gate](execution-sessions.md#tool-execution-boundary): there is no Guard check,
no auto-mode review and no permission rule for it, and `--yolo` is irrelevant.
That is the trade-off of `native`; put a service first to keep every search
under the Guard. Providers bill searches separately from tokens (for example
OpenAI and Anthropic $10 per 1,000); AICE's cost estimates do not include
them.

Each search step is recorded in the assistant message as a `web_search` part:
the action, queries or page, reported sources, any error code, and the
provider's items unchanged. They are sent back only to the same provider, API
and model in a request that enables hosted search, which is what the provider
accepts; any other request drops them and keeps the answer text. DeepSeek
Flash receives its steps back but may not recognize them as its own searches.
The TUI, transcripts and ACP show each step as a finished `web_search` entry
labeled as run by the model provider, with its sources; compaction summaries
include them. An Anthropic `pause_turn` during a long search is resumed by the
Loop. `deepseek_web_search` reserves the function names `find`, `search` and
`open`, so the Responses adapter sends AICE's `find` as `aice_find` and maps
calls back. Implementation: [llm/web_search.go](../internal/llm/web_search.go)
and the [Responses](../internal/api/openairesponses/web_search.go) and
[Anthropic](../internal/api/anthropic/web_search.go) adapters.

## Not included in this version

The built-in Web path has no Exa Answer/Research,
DeepSeek Fetch API, automatic failover, connection tests, JavaScript rendering,
authenticated pages, PDFs or images. Fetch uses only standard proxy environment
settings. Independently configured [MCP services](mcp.md) may expose their own
search tools; they do not participate in this priority list.
