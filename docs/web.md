# Web search and fetch

AICE ships two built-in web tools. `web_search` sends a query to a configured
independent search service; `web_fetch` retrieves one public page directly.
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
| Entry references an unknown instance, unknown provider/API, or invalid options | Configuration error: search is unavailable with the exact key path; no other service is chosen |
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
  `exa-rest` and `https://api.exa.ai`. `base_url` must be an https origin
  without userinfo, query or fragment; `http` is accepted only for loopback
  development gateways and is shown as a non-standard endpoint.
- `credential` is exactly one of `{"env": "NAME"}` or
  `{"auth_ref": "web_services.<id>"}`. Keys are never stored in
  `settings.json`. `auth_ref` reads the `web_services` object in
  `~/.aice/auth.json`, which `/web` writes when you enter a key.
- Instance IDs match `[a-z0-9][a-z0-9_-]{0,63}`. The same provider can appear
  several times with different IDs, endpoints and credentials; they never share
  keys.
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

Saved changes patch only the touched instance, priority or switch under the
shared settings lock; other keys and other instances are preserved. A malformed
existing `web` object is left unchanged and the save fails. A credential saved
to `auth.json` before a failed settings save is reported as a credential-only
success; the current Session keeps its previous snapshot.

## The `/web` command

`/web` opens a menu that can show status, add an Exa instance (key entered
hidden and saved to the auth store, or a reference to `EXA_API_KEY`), replace
an instance credential, remove an instance and its stored credential, move a
priority entry up or down, remove or append entries (including `native`), and
toggle search or fetch. New instances are appended to the end of the priority
list so an existing order is never overtaken. Instance IDs are assigned
automatically (`exa-main`, `exa-2`, …) and can be renamed in the file.

Changes take effect for the next response: AICE publishes a new configuration
snapshot, rebinds the tools and refreshes the system prompt. Changes are
refused while a response is running so an approval never targets a different
service than the one shown. `/web` never contacts the search API; there is no
connection test. Opening menus, reordering entries and redrawing the screen
send no requests. Settings → Tools & Network hosts these actions and adds
result count, positive search/fetch durations, priority/domain list forms, and
per-instance enabled state, endpoint, credential reference and Exa mode fields.
A list is committed as one array; an instance edit patches only named fields.
The panel requires allow/exclude lists to be mutually exclusive without changing
the existing file-policy interpretation. User preferences and project tightening
are shown separately. Backend preparation failures prevent saving; successful
saves publish tools, prompt and Guard target together at an idle boundary.
See [Settings](configuration.md#settings-window).

## Permissions

Enabled web tools access the network automatically without an extra
confirmation. There is no per-call, per-origin, per-project or per-Session
web authorization: new projects, new Sessions, `/new` and restarts add no
web approval, and project trust stays separate from web use. `--print` uses
the same policy as interactive mode, so an enabled web tool works without
`--yolo`.

- `web_search` runs when a search service is bound (instance ID plus
  endpoint origin, set by the application per run). Without a binding it
  denies and sends no request. Disabling search, an unusable priority list,
  a configuration error or missing credentials keeps the tool unregistered
  or unavailable; default allow never enables the tool or configures a
  provider by itself.
- `web_fetch` runs when the URL passes the shared shape check. Malformed
  URLs, userinfo, zone-scoped IPv6 literals and non-default ports deny
  before any request. Disabling fetch unregisters the tool.
- Explicitly rejected targets, body limits and the configured service list
  still apply; `--yolo` never lifts a deny. A denied or cancelled call
  sends no request. `/btw`, compaction and `/init` remain tool-free.

Enabling a web tool means the model can cause automatic network access
through that tool. The limits below bound `web_fetch` only; they are not a
process-wide network sandbox.

## `web_fetch` limits

`web_fetch` accepts absolute `http`/`https` URLs on the default ports (80/443)
without userinfo or zone-scoped IPv6 literals. An explicit `localhost` name or
a blocked IP literal is refused before any request: loopback, private,
link-local, CGNAT, multicast, unspecified, IPv4-mapped IPv6, NAT64, 6to4 and
documentation ranges, including the link-local metadata address. Hostnames
that are not literals are left to the standard HTTP transport (and the proxy
side, when one applies) to resolve; AICE performs no DNS pre-resolution and
pins no addresses. TLS verifies the URL hostname. Each redirect hop (at most
5) is revalidated under the same URL-shape and literal-target policy, with
no `https` to `http` downgrade. Legal cross-origin redirects continue
automatically; dangerous targets, downgrades and over-limit chains still
fail.

Requests use a clone of `http.DefaultTransport`, so the standard proxy
environment (`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` and their lowercase forms)
applies and `NO_PROXY` bypass works as usual. AICE does not choose a network
exit, implement `CONNECT`, branch between direct and proxy paths, retry a
proxy failure over a direct connection, or add proxy settings, menus or
commands. The fetcher shares no cookies, headers or credentials with the
model or search clients. Raw and decompressed bodies are limited to 5 MiB
each; the whole
fetch, including redirects and body reading, is bounded by `web.fetch.timeout`
(default 30 s). Supported content types are `text/plain`, `text/markdown`,
`text/html` and `application/xhtml+xml`; missing or generic types are sniffed
within 512 bytes and anything else is `unsupported_content`. Charsets declared
in headers, a BOM or an HTML meta tag are converted to UTF-8.

HTML is parsed into a DOM. Scripts, styles, frames, embeds, navigation, asides
and footers are removed; the `<main>`, `<article>` or `<body>` element is
selected and the method is reported. Headings, paragraphs, links (resolved
against the final URL, ignoring `<base>`), lists, code blocks, tables and quotes
are converted to Markdown or plain text. This is a basic extraction, not a full
readability engine. Extracted text retained as evidence is limited to 32 KiB
with an explicit truncation flag; the model-facing output is also bounded.

These protections bound `web_fetch` only. Other tools, in particular `bash`,
keep the network access of the AICE process; disabling web tools does not take
the Agent offline.

## Results and evidence

Search results and fetched pages are returned to the model as deterministic
text marked as external, untrusted content. Search output lists numbered
results with title, URL, optional published date and evidence excerpts, followed
by a reminder that excerpts are not the full page. Output never contains
timestamps, request IDs or durations, so identical results stay identical for
repeated-tool detection while changed results count as progress. Terminal
control sequences are stripped from all upstream text.

Each result also records structured evidence in the tool result message:
sources (deterministic ID derived from the normalized URL, URL, title, and a
published date only when the upstream supplied one) and evidence items with a
kind (`snippet`, `excerpt`, `document`, `summary`), text, format, acquisition
(`search_service`, `http_fetch`), retrieval time and truncation. Exa highlights
become excerpts and are never merged with generated summaries. Operational
diagnostics (upstream request ID, warnings, bytes, reported cost when the
service returns one) stay out of the model text. Evidence is bounded to
64 KiB, is cloned across ownership boundaries, persists in the same Session
record and is restored on resume, checkout and history browsing. Provider
adapters send content only. The TUI lists the recorded sources beneath the tool
output; print modes carry the same content text.

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

The adapter was written against the [Exa Search API reference](https://exa.ai/docs/reference/search)
as published on 2026-09-21. Default tests use local fixtures only. A real
request requires the `integration` tag and an explicit opt-in; see
[Verification](collaboration.md#web-checks).

## Not included in this version

Model-native search, a second real search provider, Exa Answer/Research
endpoints, automatic failover between services after a failed request,
connection tests, JavaScript rendering, authenticated pages, PDFs and images,
custom proxy configuration for `web_fetch` (it only follows the standard
proxy environment), and MCP search tools. Native search will reuse
the same priority list and evidence contract; see
[Architecture](architecture.md#planned-extensions-and-restraint).
