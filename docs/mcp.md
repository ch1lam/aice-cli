# MCP

## Implementation status

Print and interactive main runs support configured stdio and Streamable HTTP
services, lazy connection, `tool_search`, resources, source-tagged server
instructions, OAuth and retained-result readback. CLI, `/mcp` and Settings use
the same application management operations. Configuration does not grant
connection or tool permission; those are separate decisions.

`internal/mcpclient` owns one bounded transport; `internal/config` owns scoped
configuration/credentials; `internal/app` owns reusable connections and a frozen
catalog per Run. The Loop owns next-round tool selection and checks Guard before
each dispatch. See [runtime contracts](contracts.md#run-local-tool-selection).

Computer Use uses this path as `managed:cua`. Its 11 reviewed operations retain
native schemas except for the host-owned session argument. AICE checks the
frozen control mode and model image capability; Cua owns native targeting,
snapshot validity and input semantics. See the [managed boundary](desktop.md#managed-mcp-boundary).

The managed row is always visible in status, including when disabled. It shows
`desktop_enabled` and navigation to Computer Use settings, not native readiness.
Status creates no desktop Run and performs no native discovery. Ordinary MCP
mutations reject this row; `/mcp desktop` opens its owning setting.

Ordinary stdio definitions cannot target AICE's managed CUA endpoint, even when
Computer Use is disabled or yolo is enabled. Add/replace and connection startup
check explicit socket/pipe paths, aliases and recognized Cua executables;
implicit default-endpoint selection is refused. An independent endpoint,
`mcp --direct` runtime or `cua-driver-local` namespace uses ordinary MCP
authorization. This reservation is not host isolation: opaque scripts, renamed
copies and HTTP proxies can hide destinations, and external programs retain
their separate authority.

## Configuration identity and storage

The loader accepts a file-only `mcp` object in user settings and trusted
project settings. Loading or saving it does not start processes, probe an
executable, connect to endpoints, or authorize either connection or tool use.
For example, this defines a public service without connecting it:

```json
{
  "mcp": {
    "servers": {
      "docs": {
        "name": "Documentation",
        "transport": "http",
        "url": "https://mcp.deepwiki.com/mcp",
        "enabled": true,
        "required": false,
        "call_timeout": "60s",
        "include_tools": ["read_wiki_structure"],
        "pinned_tools": []
      }
    }
  }
}
```

Server IDs use lowercase letters, digits, hyphens and underscores, start with
a letter and are at most 64 characters. `cua` is reserved for the managed
Computer Use instance. A stdio definition uses `transport: "stdio"`, an
absolute `command`, explicit absolute `cwd`, `args`, and optional `env`.
HTTP and stdio fields cannot be mixed. Connect/call timeouts default to 15s/60s;
configured values must be positive and no longer than 24h. Each collection is
limited to 64 servers and a 1 MiB serialized settings object.

`include_tools`, `exclude_tools`, and `pinned_tools` contain exact remote names.
Omitted includes permit the catalog; an explicit empty include list permits no
tools. Exclusions win and pins must be included and not excluded. These are
eligibility filters, not execution grants.

User and project entries with the same ID remain separate. Their source
location participates in identity; fields are never merged between them.
Removing a user entry does not select a same-name project entry as a fallback.
An untrusted project supplies no entries or restrictions. The `restrictions`
array accepts only denies: `source` is `user`, `project`, or `*`; `server` is
an ID or `*`; `tools` lists exact denied tools, or denies the whole service
when empty/omitted. User and trusted-project restrictions accumulate. Projects
cannot remove user restrictions or enable a disabled user-owned definition.
The run catalog and connection owner both apply the relevant restrictions.

Environment values and HTTP header credentials use explicit references. For
example, `"headers": {"Authorization": {"env": "DOCS_TOKEN", "prefix": "Bearer "}}`
uses a named environment variable; `{"auth_ref":"token","prefix":"Bearer "}`
selects an MCP credential slot. Stdio `env` additionally accepts literal
`{"value":"constant"}` values. HTTP headers cannot contain literal credentials
in settings. References resolve only against the environment frozen at load
time (when environment input is enabled) or their exact MCP credential scope.
Missing/invalid values stay visible by field name; they are not filled from
model-provider credentials. The connection owner reports `needs_auth` and does
not open the connection until all required references resolve.

Slots live under `mcp_services` in the user auth file, keyed by source-qualified
server and a connection-configuration fingerprint. The same slot name on another
source, endpoint, command, argument list, working directory, or reference does
not retrieve that credential. A second effective fingerprint also covers
resolved values so a credential/account change cannot reuse an old grant key.
OAuth uses a stable login identity instead of rotating token bytes; its separate
credential records and identity rules are described below.
Fingerprints prevent accidental identity reuse; they do not prove server behavior.
Resolved secrets are private snapshot fields and omitted from JSON/display
representations; only the connection owner explicitly obtains copied values.
Definitions in the auth file and credential namespaces in settings/project
files are ignored with a diagnostic.

The shared locked writer patches only selected user entries/toggles or one
credential slot, preserves unrelated values and reports atomic commit status.
Runtime snapshots retain frozen project restrictions and environment input;
ordinary preference edits preserve MCP state. There is no file watcher,
credential fallback, execution during configuration loading/saving, or separate MCP store.
Explicit cross-Session tool rules use the same user-owned auth file; see
[permanent user permissions](#permanent-user-permissions).

Connection decisions live under `mcp_connections` in the same user auth file.
Each source-qualified service key has one `{fingerprint, decision}` record,
where `decision` is `allow` or `deny`. The effective fingerprint covers source,
connection parameters and resolved credentials; mismatches ask again. Settings
and project files cannot provide this namespace and are ignored with a diagnostic.
The configuration API saves/removes a decision atomically without connecting or
granting tool execution. Saved decisions are bounded to 1,024 entries/1 MiB.
They store fingerprints, not resolved credentials. Session tool grants stay in
memory; `--yolo` never persists either kind of approval.

## Management CLI

`aice mcp` works without a model credential, Session, helper installation or an
Agent run. Results are JSON. `status` is a configuration/local-state read and
never connects; zero counts with `catalog_known: false` mean unknown, not an
empty remote catalog. `connect` and `reconnect` explicitly initialize and list
tools, report discovered/eligible counts, and close their owned connection on
exit. They call no remote tool and do not leave a background connection alive.

```sh
# server.json contains one server definition, not the enclosing mcp object.
aice mcp add docs < server.json
aice mcp status user:docs
# Review the printed source, connection fields and fingerprint first.
aice mcp approve user:docs --fingerprint '<fingerprint from status>'
aice mcp connect user:docs
```

| Command | Behavior |
| --- | --- |
| `status [KEY]` | Show source-qualified IDs, configured fields, effective fingerprint, approval and local status; resolved credentials are omitted |
| `add ID` | Read one JSON definition from stdin, save a new user entry; duplicate/concurrent additions cannot replace it |
| `replace KEY` | Read a full replacement user definition from stdin; connection changes require new approval |
| `enable KEY`, `disable KEY` | Change only the saved user toggle; enable is not a connection or tool grant |
| `remove KEY` | Remove stored credentials/connection decisions/user rules for that source-qualified service, then its user definition |
| `approve KEY`, `deny KEY`, `forget KEY` | Save allow/deny or remove the decision; require the exact current `--fingerprint` |
| `credential KEY SLOT` | Read a referenced credential slot from stdin with `--fingerprint`; `--clear` removes the slot without reading stdin |
| `login KEY` | With the exact current `--fingerprint`, discover OAuth, register if needed, open browser consent and save the new login; `--no-browser` prints the URL without opening it |
| `logout KEY` | With the exact current `--fingerprint`, remove OAuth credentials, connection approval and user tool rules; retain explicit client-secret slots |
| `connect KEY`, `reconnect KEY` | Test initialization and supported catalog discovery using an existing approval; no implicit yolo or new grant |
| `permissions KEY` | Explicitly initialize/discover operation names and schema fingerprints after connection approval; never call a tool or read a resource |
| `permission KEY` | Save one exact user rule with the reviewed fingerprint, permission scope, operation, name and schema; does not connect |

Connection tests discover tools when supported, otherwise list resources. They
report a resource-only service as ready without reading any resource. A service
with neither capability can initialize successfully while reporting its lack of
supported operations; an incomplete advertised catalog remains an error.

Definitions are bounded to 1 MiB. Secret input is bounded to 8 KiB, is not taken
as a positional argument, and never appears in the command result. Credential
slots must already be referenced by the selected service; model-provider tokens
are not reused. Changing a resolved credential changes the approval fingerprint.
Stdio definitions can be saved before their executable exists. A later failed
test leaves that saved configuration intact, including when startup fails before
a client is created.

All MCP subcommands accept `--workspace`, `--trust-project` and
`--no-trust-project`. The two trust flags are mutually exclusive. Project Trust
controls whether project definitions load; it grants no connection permission.
Project definitions are read-only through these commands. Their scoped
credentials and connection decisions can be managed in the user auth file;
use `deny` to block a reviewed project connection without editing its source.

Saves use the existing atomic writer. Service removal spans two existing files:
it clears stored access first, then removes the definition. If the second write
fails, the result reports `committed: true` and says the definition was not
removed; it does not claim a cross-file transaction. Retry removal after fixing
the settings error. Separate running AICE instances retain their frozen
configuration; these commands do not revoke another process's in-memory grants.

## Permanent user permissions

Ordinary approval prompts offer only once/Session scopes. To save a rule across
Sessions, explicitly use the management CLI or `/mcp permission KEY` (also
available from Settings). `mcp permissions KEY` performs bounded discovery and
reports each operation's raw name, schema fingerprint, configured eligibility
and current rule decision. It requires connection authorization and performs no
tool call or resource read. A rule decision is not evidence of task safety or
unchanged server behavior; remote annotations never create rules.

```sh
aice mcp permissions user:docs
aice mcp permission user:docs --tool read_wiki_structure \
  --fingerprint '<connection fingerprint>' \
  --scope '<permission_scope from status>' \
  --schema-fingerprint '<schema fingerprint from permissions>' \
  --decision allow
```

`--decision deny` blocks that operation even under yolo; `--decision ask` removes
its saved rule without connecting. For resource reads use both
`--operation resources/read` and `--tool resources/read`: the rule covers **all
resource URIs on that service**, independently of an ordinary tool with the same
name. No wildcard or permanent whole-service tool grant is inferred.

Rules live under `mcp_permissions` in the user auth file. One bounded set per
source-qualified service binds the connection fingerprint and configured
include/exclude/restriction scope, with one rule per operation/name. An allow
matches the exact input/output schema fingerprint; a deny continues to block
schema changes within that connection and scope. Disabled services, configured
restrictions and revoked connections always win. Settings/project files cannot
supply these rules. The namespace is bounded to 1 MiB, 1,024 service sets and
2,001 operations per set; malformed rules fail closed without being overwritten.

A different connection, account or configured scope makes saved rules inactive.
Status shows their stored fingerprint/scope separately from the current binding;
inactive rules can still be removed. They are exact-binding configuration, so
restoring the identical binding/schema can make an explicit rule applicable
again; this never restores a transient Session grant. Saving a rule for a new
binding replaces the previous binding's set. Service removal and OAuth
login/logout erase that service's rules; token refresh within one OAuth login
preserves them. Session replay cannot create rules.

The interactive editor discovers candidates, then shows source, connection,
operation, schema and scope before saving. Cancel leaves permissions unchanged;
discovery may already have initialized the approved service. Saving requires an
idle application and retires the affected service lease, invalidating its old
selected tools, Session grants and pending permits. Other services are retained.
CLI writes apply to subsequently loaded configurations; other
running processes keep their frozen snapshots. `status` remains an inert read,
and removing a rule is possible when the service is offline.

## Interactive management

The MCP services and authorization settings menu groups actions into status,
service configuration, connection authorization, credentials/login and operation
permissions, with a separate Computer Use shortcut. Esc returns from a group
to the main menu. Details, confirmations and action results use the MCP display
highlights while preserving the complete paged authorization disclosure.

Settings MCP status details group each service into connection, tool catalog,
permissions and optional diagnostics, followed by shared notes. The TUI highlights
section headings, field labels and connection/approval states; long values wrap
and the detail view scrolls without dropping fingerprints or saved rules.

`/mcp` and Settings → Tools & Network → MCP services and authorization expose
the management operations above. `/mcp ACTION [KEY]` selects one directly;
missing keys open a source-qualified selector. Add/replace uses a transient
JSON editor; credentials use a hidden editor. These prompts and operations do
not enter Session history or model context. Project definitions are read-only.

Configuration changes, permissions, login/logout and reconnect require idle
state. After a committed save, only affected service leases and Session grants
are retired; reconnect also retires its selected lease when configuration is
unchanged. Unrelated services survive. Global restriction changes also invalidate
managed Computer Use. A later runtime preparation failure retains the saved
change and stops new runs until repair/restart. Partial access removal is applied
even when removing the definition subsequently fails.

Status, deny and Computer Use navigation remain available during a Run. Deny
saves the current connection decision, blocks new dispatches and cancels/closes
that service's operations. It cannot undo dispatched effects; an unreceived
outcome stays unknown. Reapproval while idle does not restore Session grants.
Other AICE processes keep their frozen snapshots; there is no live cross-process
revocation or configuration watcher.

Status distinguishes unknown from empty catalogs and reports discovered,
eligible and loaded tool counts. `loaded_tools` is the latest model request's
schema count in the current main Run, including pins/resource readers/CUA;
`run_active` distinguishes that snapshot from idle. Revocation blocks execution
before that display count changes. Completion, cancellation, failure, `/new`
and Session switching clear it. CLI status has no active Run and reports zero.
Counts and status reads perform no discovery and grant no authority.

## OAuth protocol support

`aice mcp login KEY --fingerprint VALUE`, `/mcp login KEY` and Settings share
explicit browser consent, callback handling and scoped persistence. Saving a
definition, chat and connection tests never start consent. CLI progress/URLs go
to stderr, JSON results to stdout; `--no-browser` displays the URL. The loopback
redirect must reach this AICE process. Interactive login also accepts the complete
redirect URL in a hidden prompt. Login has a 15-minute deadline.

Supported: authorization code with S256 PKCE, pre-registered public or
client-secret clients, and dynamic public-client registration. Hosted client-ID
metadata documents, private-key authentication, device codes and proprietary SSO
are unsupported. Multiple advertised issuers require explicit selection.
Discovery supports resource metadata, RFC 8414 and OIDC well-known paths;
only 404/405 permits fallback. Identity mismatch or malformed metadata fails
closed. HTTPS is required except local loopback HTTP; HTTPS cannot downgrade.

HTTP definitions accept `oauth` instead of an `Authorization` header. `{}` selects
dynamic public registration. A pre-registered client supplies `client_id`, a
literal-loopback `redirect_uri` with explicit port, and
`token_endpoint_auth_method` (`none`, `client_secret_basic` or
`client_secret_post`). Confidential clients require an `env`/`auth_ref`
`client_secret`, without a prefix. `issuer` selects a discovered issuer;
`scopes` distinguishes omitted from explicitly empty. Stdio OAuth is unsupported.

The listener is bound before registration and closed on completion/cancellation.
Callback validation checks redirect, random state, duplicates and issuer; an
accepted callback consumes the attempt even if exchange fails. Requests do not
follow redirects or replay bodies. Protocol requests/discovery have 30-second
deadlines, 1 MiB JSON limits and sanitized errors. Cancel/denied consent saves
nothing; a remotely registered client is not automatically deregistered.

### OAuth credential state

`mcp_oauth` records live only in the user auth file, scoped by source-qualified
service and connection configuration. Login binds resource, issuer, token
endpoint, client and callback, then creates a new stable grant identity and
removes prior connection approval and service rules. Review/approve the new
fingerprint before connecting. Token rotation preserves that identity; changing
the endpoint, client, configured scopes or resolved client secret does not.

Logout removes local OAuth credentials, connection approval and user rules,
retaining explicit client-secret slots. Service removal clears those slots too.
Neither operation claims remote revocation or signs out other running processes.
Only the access-token Bearer header reaches the resource; client/refresh secrets
remain at the auth boundary. Known credentials are redacted from results.

### Automatic refresh before operations

After checking connection authorization and acquiring the service gate, the app
refreshes expired credentials before opening, listing or calling. Construction,
status, denied work and unknown expiry do not refresh. The existing auth-file
lock coordinates processes: adopt a prior rotation or perform one exchange
against the saved identity binding, then persist before use. Refresh has a
one-minute writer deadline, further bounded by the operation deadline.

The transport reads a local header snapshot and performs no OAuth I/O. Rotation
preserves its connection, catalog and grants. Final dispatch rechecks expiry;
expiry while queued rejects without dispatch or automatic replay. Cancellation
or later publication failure cannot undo an already persisted rotation.

Refresh failure or HTTP 401/403 stops automatic attempts in that owner and shows
`needs_auth`; explicitly reconnect or log in after repair. An uncertain dispatched
RPC stays uncertain. Retained token values remain available for redaction within
a bounded history; exhaustion requires reconnect. Another process's logout is
noticed at refresh, not immediately by an owner with a still-valid frozen token.

Implementation: [protocol](../internal/mcpauth),
[persistence](../internal/config/mcp_oauth.go),
[application refresh](../internal/app/mcp_oauth_refresh.go).

## Bound execution permission

The app binds model names to source, service, connection fingerprint, permission
scope and schema version. Model arguments, name prefixes, annotations and saved
Session provenance cannot construct this authority. An eligible new operation
asks; choices are once, that tool version for this Session, or the service's
currently eligible versions for this Session. A service grant excludes later
catalog additions. Noninteractive asks fail closed; yolo bypasses ask, never deny.

Unchanged catalog refresh preserves grants. Schema/tool-policy changes clear the
affected grant; connection, scope, enablement, configured filters or user-rule
changes invalidate that service's grants. Reverting settings cannot restore a
transient grant. Revocation survives catalog refresh. `/new` and successful
Session switching clear Session grants and outstanding permits, preserving
explicit user rules.

After approval, the Loop rechecks its request mapping and current policy; the
transport queue rechecks the catalog and call-local permit immediately before
dispatch. A changed catalog during service-wide approval rejects the whole grant.
No Guard lock is held during prompts or execution. This prevents stale local
authority, not remote side effects or changed server behavior. An operation
already dispatched cannot be undone by revocation.

## Run catalog and tool search

Each main Run gets a frozen service catalog borrowing application-owned lazy
connections. It starts with builtins, `tool_search`, `mcp_resource_list`,
`mcp_server_info` and a bounded service summary. Required services and pins share
a ten-second preparation deadline; missing/denied services or unavailable pins
fail before the first model request. Optional services perform no I/O until
discovery. A new Run does not inherit selected definitions; `/btw` stays tool-free.

`tool_search` supports keywords, source-qualified service browsing, up to five
exact IDs and `next_offset` paging. Search is lexical: names/descriptions,
limited English normalization, overlapping Chinese character pairs and a small
fixed action vocabulary. Complete remote-name matches rank first; ties use stable
IDs. It does not translate or infer arbitrary synonyms. Preserve original-language
keywords, refine mismatches or browse/select exactly. Changed catalogs can move
page boundaries. Ranking has no connections or execution authority.

Discovery has a ten-second deadline and up to four concurrent service requests.
The owner admits eight connections; each Run allows 129 service bindings,
16,000 eligible tools and 32 MiB catalog data. Failed/partial/invalidated discovery
removes that service's stale Run entries and reports incompleteness. Unknown
catalogs are not empty catalogs.

Stable IDs combine source-qualified service and escaped remote name. Model names
include a readable prefix/hash and are collision-checked. Immutable versions bind
connection, schemas and catalog generation. Notifications invalidate versions
before dispatch. Search proposes typed references; only the Loop installs full
schemas after recording a complete tool-result group, under its schema budget.
Search text and Session replay cannot select/authorize tools. Run cleanup closes
its catalog; borrowed transports remain application-owned.

## Server usage instructions

Search includes source-tagged instruction previews only from already initialized
services represented by its candidates: at most 512 UTF-8 bytes each/five services,
within the whole 32 KiB search result. Previewing performs no additional RPC.

`mcp_server_info` accepts an exact source-qualified `service`, plus optional
`offset`, `length` and `revision`. It initializes only that approved service and
reads retained initialization data, without listing/calling tools, reading
resources or fetching links. It returns source/connection identity, server
version/protocol/capabilities and a separate instruction text block. Managed CUA
requires prior managed discovery.

Pages default to 1,024 bytes, maximum 8,192. Continue using `next_offset` and
`revision`; invalid UTF-8 boundaries fail and changed revisions restart at zero.
Known credentials are redacted before paging. Metadata/raw-text bounds reject or
mark oversized data. Read pages enter ordinary Session results; unread instructions
remain connection data and may change on reconnect.

Remote text is untrusted usage data, never system instructions or execution
permission. Advertised Prompts remain unsupported; remote Skills are not installed
or activated. Existing local Skill trust and loading rules apply.

## Resource discovery and reading

`mcp_resource_list` accepts an exact source-qualified `service`, optional `offset`
and `limit` (default/maximum five). After connection authorization it lists only
that service, under client bounds and a ten-second deadline. It returns metadata
and `next_offset`, never reading resources/following links/calling tools.
Unsupported, unavailable and incomplete catalogs are explicit failures/notices,
not empty success. Pages rediscover, so changing catalogs can shift offsets.

Successful listing proposes one `mcp_resource_read_<hash>` schema through the
same next-round selection budget. Its catalog ID is `<service>/resource/read`.
The reader sends one exact `uri` (up to 4,096 UTF-8 bytes) only to that server;
it never interprets the URI as a local path/HTTP target. URIs from returned links
are also accepted. Templates, subscriptions and MCP Prompts are unsupported.

Reading asks independently of connection approval/listing. Once/Session choices
apply, with a resource-reader grant covering **all URIs on that service**. Tool
include/exclude filters do not filter resource URIs; disable/revoke the service
to block resources. There is no URI-specific permanent permission. Ordinary tools
named `resources/read` use a different authorization domain.

Tool and resource notifications/policy refresh invalidate their own references
independently; service revocation invalidates both. Final queued dispatch rechecks
binding and permit. Results use ordinary ordered MCP mapping, loss markers,
Session retention and readback. Failures/unknown outcomes never replay reads,
and returned links are not expanded automatically.

## Connection ownership

The app owns configured transports for one Print invocation or interactive
application. States are `disconnected`, `connecting`, `ready`, `needs_approval`,
`needs_auth`, `failed` and `disabled`. Opening checks current restrictions and
exact saved connection approval; missing credentials never reach transport.
Status uses local sanitized facts and never connects optional services.

Operations serialize per service, including queue time in the call timeout
(default 60 seconds), without holding bookkeeping locks across I/O. Searches
reuse connections. Failed initialization releases its slot and later discovery
may retry; tool dispatch never initializes or reconnects. An established failed
transport requires explicit reconnect. Interactive reconnect replaces only the
selected lease; existing borrowed views cannot acquire its replacement by name.

Revocation cancels active/queued operations and invalidates versions before
close. Application exit joins work and closes late-returned clients. Only owned
transports/children are closed; already dispatched effects remain returned or
unknown and are never replayed.

`mcpclient.Open` initializes one caller-supplied stdio/HTTP connection without
loading settings, installing software or granting authority. SDK types stay
private. Stdio requires absolute executable/cwd; arguments have no shell
expansion. Only a small base environment and explicit additions are inherited,
not provider credentials. Stderr is discarded. Close uses bounded pipe/process
cleanup on its exact child, not a process group/shared daemon. Cancellation of
an active stdio operation closes that connection.

HTTP requires HTTPS or loopback HTTP and rejects URL userinfo. It disables
redirects, body replay, idempotency headers, SSE reconnect and SDK OAuth replay.
Errors expose sanitized status/classifications. Close attempts a one-second
session DELETE then always cancels local lifetime/idle sockets; a failed DELETE
returns `ErrTransport` and does not establish remote session deletion. Repeated
close does not retry DELETE.

Internal consumers can pin protocol and replace the complete stdio environment;
ordinary services use SDK negotiation. Only Cua pins `2025-06-18`. The client
advertises no roots, sampling or elicitation, and exposes no Prompts operations.
OAuth/approval remain owned outside transport.

## Discovery and results

Tool/resource pagination returns caller-owned values, generation, completeness
and notices. List notifications invalidate generations before dispatch.
Duplicates, repeated cursors, invalid entries, changed pagination and failed pages
are incomplete discovery, not empty success. The client checks schema object
shape; the tool adapter owns the effective model/argument contract.

Default hard limits are 16 MiB per incoming JSON/SSE frame, 4 MiB catalog pages,
2,000 entries, 20 pages and 1 MiB arguments. Internal callers may raise frames to
24 MiB or lower limits. Initialization defaults to 15 seconds and operations to
60 seconds including queue time. HTTP also bounds dialing/headers/cleanup.

Calls and resource reads dispatch once. Pre-dispatch validation/cancellation is
`not_dispatched`; an observed result/protocol error is `returned`; a failure after
a write may have begun is conservatively `unknown`. Timeouts, cancellation,
`isError`, HTTP failures and disconnect do not replay operations.

Raw frames preserve exact structured numbers before SDK floating-point decoding.
The tool adapter retains ordered text, validated images, embedded resources,
labeled links, structured JSON, error status and bound execution provenance.
Session's [structured source companion](contracts.md#structured-tool-outcomes)
preserves spelling/whitespace where needed for identical replay. Generic media
retains image originals and displayed-size information.

Source ceilings are 256 blocks, 1 MiB aggregate text, 1 MiB structured JSON and
16 MiB image views/originals. Excess, invalid images, unsupported audio/binary
and unfamiliar content receive explicit loss notices. Local media preparation
has a separate five-second deadline so cancellation after an RPC does not erase
received text/JSON. Known credentials are redacted across text/resource metadata
and decoded JSON values; unsafe redaction or credential-bearing schemas/images
are rejected or marked lost. This is not detection of secrets drawn in pixels or
arbitrarily encoded. Source loss cannot be recovered by context readback.

## Model views and result readback

The Loop records the source result before constructing the next model request.
Only its derived request view is clipped: each result carrying `details` gets
10% of the model context window, capped at 4,096 estimated tokens with a
256-token minimum; an unknown window uses 4,096. These are local estimates,
not a provider tokenizer guarantee. Legacy results keep their existing tool
limits. Total context still follows ordinary safe-boundary compaction.

The view preserves execution state, error status and source block order. Text
prefixes end on UTF-8 boundaries. Before filling the view with text/images, it
reserves up to 512 tokens and one quarter of the available budget for structured
data (the full remaining budget for structured-only results). Whole JSON remains
structured data; otherwise a separate text block shows an explicitly incomplete
raw UTF-8 prefix. It preserves numeric spelling and is never presented as a
complete JSON document. Unused content budget can extend that preview.
Images consume the existing 1,200-token estimate and require at most 4 MiB of
view bytes. A clipping notice supplies callable `tool_result_read` JSON arguments
with the source's exact `call_id`, selecting structured data when present or
metadata otherwise. If an unusually long selector cannot fit, the notice points
to the exact ID in the tool-call envelope instead of inventing a shortened ID.
Long source-loss notices are abbreviated only in this view. Neither clipping nor readback changes
source loss into recoverable data. Current request estimates include this view,
instead of reusing provider usage for a different result projection. Compaction
input carries explicit execution/loss metadata and bounded JSON/text, while
source records remain untouched.

The built-in `tool_result_read` is available even without configured MCP services.
It resolves exactly one `call_id` or `entry_id` against the current run's active
Session ancestry, including source entries before compaction. A repeated call ID
is rejected with candidate entry IDs rather than selecting an arbitrary result.
Inactive branches, other Sessions and model-selected filesystem paths are not
readable. Saved bindings are provenance only; reading them does not reconnect,
re-authorize or replay the original operation, nor fetch returned links.

Default `section=metadata` reports entry identity, durability, execution state,
source loss and up to 32 block descriptors. Pass `next_block` as `block` for the
next metadata page. `section=content` reads one indexed text/image block;
`section=structured` pages the exact saved JSON bytes as text, preserving numeric
precision. Structured pages are marked `format: raw_json_text` and `fragment`
when they are not the entire document. Text uses UTF-8 byte offsets and lengths
up to 8,192 bytes, with `next_offset` for continuation. Both text and metadata
pages provide `next_read` with exact continuation arguments, using the resolved
entry identity so repeated call IDs do not make subsequent pages ambiguous.
Metadata is capped at 32 KiB and a selected
image at 16 MiB; unsupported saved kinds produce an explicit error. Readback
runs through Guard as a local built-in and adds an ordinary paired result.

Interactive runs and Print with `--session` read the append-only Session and
report `durable: true`. Print without `--session` retains its existing ephemeral
behavior: readback uses that invocation's already-owned source messages, reports
`durable: false`, and cannot recover them after process exit. There is no implicit
Session creation or second result store. Server usage pages have their separate
connection-bound reader described above; previously read pages remain ordinary
retained tool results.

## Verification evidence

Offline tests use local processes, loopback HTTP peers, temporary settings and
synthetic credentials. Representative boundaries are [application Loop/Guard](../internal/app/mcp_loop_test.go),
[owner lifecycle](../internal/app/mcp_owner_test.go),
[permissions](../internal/app/mcp_permissions_test.go),
[OAuth refresh](../internal/app/mcp_oauth_refresh_test.go),
[resources](../internal/app/mcp_resources_test.go),
[instructions](../internal/app/mcp_info_test.go) and
[result readback](../internal/app/result_read_test.go). They do not establish
external-account, real-model or native desktop acceptance.


Recorded interoperability covers one Filesystem stdio service, one DeepWiki
HTTP service and Linear read-only OAuth login/refresh on macOS. Filesystem and
DeepWiki use a scripted model and Session reopen; Linear's refresh test advances
local expiry, so natural expiry and other OAuth providers remain unverified.
A later DeepWiki close failed after successful reads; local cleanup tests prove
bounded close, not remote session deletion. Lexical retrieval does not translate
queries or establish general model-quality/performance gains.

Reproduction commands belong to [Verification](collaboration.md#real-mcp-interoperability)
and [retrieval checks](collaboration.md#mcp-retrieval-checks). Native Computer Use
limits are in [platform evidence](desktop.md#platform-evidence).
