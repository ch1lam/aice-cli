# Web search and fetch

AICE ships two built-in web tools. `web_search` sends a query to a configured
independent search service; `web_fetch` retrieves one page through HTTP.
Both are ordinary tools: they pass through the execution gate, produce a paired
tool result, and record their sources in Session history. Neither depends on
the model provider supporting search natively.

## Search sources and priority

`web.search.priority` is an ordered list of sources. Two entry kinds exist:

- `native` — model-native search. It is reserved and can be ordered anywhere,
  but this version has no native implementation, so it is always skipped with
  the reason `not_implemented`.
- `service:<id>` — an instance configured under `web.services`.

At the start of each run AICE resolves the first usable entry. The resolver is
static: it checks configuration, enabled state and credential presence, never
network reachability. Rules:

| Configuration | Outcome |
| --- | --- |
| Default (no `web` object) | `priority` is `["native"]`; no search tool is registered, coding tools work unchanged |
| `["native", "service:exa-main"]`, Exa ready | Exa is used; `/web` shows that `native` was skipped as not implemented |
| `["service:exa-main", "native"]` | Exa is used even if native search becomes available later |
| `[]` | No search source is allowed; `web_search` is not registered |
| Invalid file schema or priority referencing an unknown instance | Configuration loading fails with the offending key |
| Resolver reaches an unknown provider/API or invalid provider options | Search is unavailable; no later service is chosen |
| First usable entry lacks its credential | `missing_credentials` is reported; AICE does not fall back to another account |
| Instance `enabled: false` | Skipped with `disabled`; later entries are considered |
| A search request fails (401, 429, timeout…) | The tool returns a classified error; no second backend is tried |
| A search returns no results | A successful empty result |

When no source is usable the `web_search` schema is not sent to the model at
all; `/web` and `/settings` show the reason. `web_fetch` is independent of any
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

- `api` and `base_url` may be omitted; the Exa descriptor supplies
  `exa-rest` and `https://api.exa.ai`. `base_url` accepts an HTTPS URL with an
  optional path prefix; requests append `/search`. Userinfo, query and fragment
  are rejected. HTTP is intended only for loopback development gateways; the
  current hostname check has a [known gap](maintenance.md#exa-http-loopback-validation).
- `credential` is exactly one of `{"env": "NAME"}` or
  `{"auth_ref": "web_services.<id>"}`. Keys are never stored in
  `settings.json`. `auth_ref` reads the `web_services` object in
  `~/.aice/auth.json`, which `/web` writes when you enter a key.
- Instance IDs match `[a-z0-9][a-z0-9_-]{0,63}`. The same provider can appear
  several times with different IDs and endpoints. Each instance has its own
  credential reference; environment references may name the same variable.
- `options.type` accepts `auto` (default) and `fast`. Deep or synthesized
  modes are rejected explicitly rather than mapped to `auto`.
- `default_max_results` is 1–20 (default 8); the model may request 1–20 per
  call. `timeout` values are AICE product defaults, not upstream guarantees.
- `allowed_domains` / `excluded_domains` are bare hostnames and act as the
  upper bound: a model request can only narrow the allow list (label-boundary
  subdomain matching) and adds to the exclude list. An empty intersection is a
  `constraint_conflict` error and nothing is sent upstream.
- `enabled: false` under `search` or `fetch` disables that tool. A missing
  `priority` uses the default; an explicit `[]` allows no source.
- Setting `EXA_API_KEY` in the environment does nothing by itself. An instance
  must be added by the user before any request can be sent.

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

`/web` opens a menu that can show status, add an Exa instance (key entered
hidden and saved to the auth store, or a reference to `EXA_API_KEY`), replace
an instance credential, remove an instance and its stored credential, move a
priority entry up or down, remove or append entries (including `native`), and
toggle search or fetch. New instances are appended to the end of the priority
list so an existing order is never overtaken. Instance IDs are assigned
automatically (`exa-main`, `exa-2`, …) and can be renamed in the file.

Changes require idle main and BTW responses and apply to the next response.
Status remains available through `/web` during a run. Menus and edits never test
the search API or send paid requests.

Settings → Tools & Network provides the same actions plus result count, timeouts,
priority/domain lists and per-instance options. It shows user preferences and
project restrictions separately. Successful publication replaces the backend,
tools, prompt and Guard binding together; prepared responses are invalidated.
The application closes superseded and exiting backends. See
[Settings](configuration.md#settings-window) and the
[Web lifecycle tests](../internal/app/web_lifecycle_test.go).

## Permissions

Enabled web tools access the network without extra confirmation in interactive
and Print mode. Search requires an application-bound service; fetch requires a
valid URL. Invalid targets still fail and `--yolo` never lifts a deny. Disabling
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
tool output. Exa highlights remain excerpts, separate from any returned summaries.

## Exa adapter

Exa is the first configured service (`provider: "exa"`, `api: "exa-rest"`). The
adapter sends exactly one `POST /search` per tool call with `query`, `type`,
`numResults`, optional `includeDomains`/`excludeDomains`, and
`contents: {"text": false, "highlights": true}`; it requests no full text,
summaries or deep-search synthesis. The key travels in `x-api-key`; redirects
are never followed so the key cannot reach another origin. Success bodies are
limited to 2 MiB and error bodies to 8 KiB. HTTP status maps to
`authentication` (401/403), `quota_exceeded` (402), `rate_limited` (429,
retaining `Retry-After`), `invalid_argument` (400/422) and `unavailable` (5xx).
Malformed JSON, a missing `results` field or an error envelope with status 200
are `invalid_response`; an empty array is a successful empty result. Paid
requests are never retried automatically. Reported `costDollars.total` is
recorded as an upstream cost in USD; a missing value stays unknown, never zero.

Default tests use local fixtures. A real request requires the `integration`
tag and explicit opt-in; see [Verification](collaboration.md#web-checks).
Implementation: [binding](../internal/app/web.go),
[configuration](../internal/config/web.go),
[Exa](../internal/web/exa/client.go), and
[fetch](../internal/web/httpfetch/fetch.go).

## Not included in this version

The built-in Web path has no model-native search, second search provider,
Exa Answer/Research, automatic failover, connection tests, JavaScript rendering,
authenticated pages, PDFs or images. Fetch uses only standard proxy environment
settings. Independently configured [MCP services](mcp.md) may expose their own
search tools; they do not participate in this priority list.
