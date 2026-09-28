# MCP

## Implementation status

The generic single-connection client is implemented in `internal/mcpclient`.
`internal/config` loads MCP service definitions and provides scoped credential
and atomic configuration operations. The Guard and application approval bridge
support identity-bound tool/Session grants and explicit cross-Session user rules. The run catalog, `tool_search`,
generic tool adapter, resource discovery/read, source-tagged server instructions,
bounded model views and retained-result
readback are wired into `--print` and
interactive main runs. An application owner lazily connects configured services
after connection authorization, prepares required services/pins, and closes its
owned transports. The `aice mcp` command manages configuration, connection
decisions, credentials, user operation rules and explicit discovery tests. `/mcp` and the Settings
MCP action use the same operations with transient prompts and live runtime
publication. OAuth protocol primitives are implemented in `internal/mcpauth`;
configuration owns scoped OAuth persistence and refresh locking. Application
management supports explicit browser login and logout. Authorized operations
refresh expired tokens under that lock before entering the MCP transport queue.
The current runtime
accepts a matching user-owned saved decision or ordinary `--yolo` ask bypass;
configuration presence alone never authorizes a connection.
The approved scope is in the [implementation plan](plans/AICE_MCP_Design.md);
the [acceptance review](plans/AICE_MCP_Acceptance.md) maps requirements to current
verification scope and remaining limitations.

Computer Use now consumes this generic transport through its pinned identity
and schema admission layer. Its admitted connection now supplies cloned tool descriptors and unchanged generic
results with catalog-generation checks. Its run-owned managed backend now projects 11 constrained operations with
owned sessions, target/image validation and final dispatch checks. Application
composition uses managed discovery for enabled Print and interactive main runs,
without exposing the typed desktop tools. An explicit app constructor
binds a native Run to the generic catalog/Guard with a reserved managed identity
and reviewed per-tool/mode permissions. Its context-bound entry accepts only the
application's live native Run; successful desktop settings publication or MCP
owner replacement invalidates old managed identities and permits, even when a
saved MCP change fails runtime preparation. The version-matched `computer-use`
Skill loads through normal discovery. MCP management shows its preference and
links to Computer Use settings. The configured connection boundary reserves the
managed native endpoint. Platform-specific validation remains limited; see
[Computer Use](desktop.md).

Managed `cua` is always visible in status, including when disabled. Its
`managed: true`, `enabled` and `settings_field: "desktop_enabled"` fields describe
the application preference and navigation target. They do not claim a connected
Driver, known catalog, OS grants or a usable screenshot. Status performs no
native inspection or discovery and does not create a desktop Run. The ordinary
connection definition is absent for this row; normal edit, enable/disable,
approval, credential and reconnect actions reject it and direct the user to
Computer Use settings. Independent configured services retain their own rules.

The application rejects ordinary stdio connections explicitly targeting the
managed CUA socket/pipe before starting their process, including `--socket=...`,
relative paths, existing symlink aliases and Windows pipe case variants.
Recognized Cua executables (including symlinks and hard links to the installed
helper) may not implicitly select the same default endpoint. Use an explicit
different endpoint or `mcp --direct` for an independent runtime; bare
Linux/Windows clients can select a daemon through mutable history preferences,
so implicit runtime selection is not proof of independence. The source-build
`cua-driver-local` namespace remains independent.

This reservation applies with Computer Use on or off, and is not lifted by
connection approval or `--yolo`. Add/replace checks resolved inputs before saving;
manually edited and trusted-project definitions are checked again at connection
time, as are connections created after Settings publication. Rejection keeps
the definition inspectable and reports a disabled connection with `/desktop`
guidance; it never starts a native task or exposes an ordinary tool catalog.
Status alone does not probe paths or connect. This is an application connection
rule, not host isolation: arbitrary scripts, renamed copies and HTTP proxies can
hide their native destination, and Bash/external programs retain their separate
authority. A different service name never grants managed Computer Use permission.

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
aice mcp permission user:docs --tool read \
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
idle application and replaces its MCP owner, invalidating old selected tools and
pending permits. CLI writes apply to subsequently loaded configurations; other
running processes keep their frozen snapshots. `status` remains an inert read,
and removing a rule is possible when the service is offline.

## Interactive management

Use `/mcp` or Settings → Tools & Network → MCP services and authorization.
Both offer status, add/replace, enable/disable, approve/deny/forget, scoped
credentials, OAuth login/logout, connect/reconnect, operation permissions and removal. `/mcp ACTION [KEY]` also selects an
action directly; omitted service keys open a source-qualified selector. Bare
`/mcp` opens the same Settings MCP menu; non-menu callers receive status.
Its **Computer Use settings** item, or `/mcp desktop`, focuses the existing
Computer Use setting. This navigation saves nothing, performs no setup, starts
no model Run and remains available during a Run. Closing/cancelling an older
panel prevents its late navigation result from affecting a newer panel.
The existing Computer Use status refresh may inspect the installed service
when that settings panel opens; this is separate from the inert MCP status read.

Add/replace accepts one server-definition JSON object in a visible transient
editor. Credential values use a separate hidden editor; they are never accepted
as slash-command arguments. Private prompts, public setup input and management
operations do not create Session records or enter model context. Approval shows
the source, configured connection and fingerprint captured under the settings
reservation. A reply must match an offered choice. Cancellation before saving
does not change the runtime. Project definitions remain read-only.

Configuration changes and reconnect require an idle application. After saving,
the coordinator closes the old MCP owner, clears MCP Session tool grants and
publishes fresh lazy connections and the next run's tool set. This replacement
currently resets **all** MCP connections, including when reconnect targets one
service; non-MCP permissions are untouched. Connect only tests the selected
existing owner. Neither action executes remote tools. Exit closes the current
replacement owner. A failure after a durable save reports the committed change;
if runtime preparation fails, new runs stop until repair/restart instead of using
the old permissions. Partial access removal is applied even if deleting the
definition fails.

Status and **deny** remain available through Settings during a run. Deny saves
the exact connection decision, revokes that service's Guard capability, cancels
its active/queued operations and closes its connection. Other services continue.
Already-dispatched effects cannot be undone and remain unknown when the result
was not received; no action is replayed. A revoked optional service cannot block
construction of the next run; required/pinned services still enforce their preparation checks.
An explicit later approval while idle rebuilds the owner without resurrecting
Session tool grants. Explicit saved rules are checked against the current binding;
other AICE processes keep their own frozen state.

Status distinguishes an unknown catalog from a known empty catalog and reports
discovered and eligible tool counts. `loaded_tools` reports the catalog Schema
count in the current main Run's latest model request, with `run_active` to
distinguish that Run from an idle app. Settings and `/mcp status` use the same
snapshot. Counts include pinned tools, resource readers and managed CUA tools;
search candidates rejected or evicted by the Loop are excluded. Revocation
blocks dispatch immediately, even while the last request snapshot still contains
its Schema; the next request drops unavailable entries. Counts grant no authority.
Completion, cancellation and failures clear the snapshot. `/new`, resumed history
and subsequent Runs cannot inherit it; `/btw` cannot publish one. A separate
management CLI has no active main Run and reports zero, not a total across other
AICE processes. Reading these counts performs no discovery or native I/O.

## OAuth protocol support

`internal/mcpauth` implements explicit protocol operations separately from MCP
transport dispatch. `aice mcp login KEY --fingerprint VALUE`, `/mcp login KEY`
and the Settings action share application-owned discovery, browser consent,
callback handling and persistence. Configuration owns scoped credentials, the
cross-process refresh lock and stable grant identity. The application refreshes
expired credentials before authorized work. Existing saved header credentials
continue to work as before.

Login requires an OAuth definition and review of its current fingerprint. It
explicitly permits contacting the configured resource, discovered authorization
server and, for dynamic clients, registration endpoint. Saving a definition,
ordinary chat and connection tests never start browser consent. The application
binds its callback listener before registration and closes it on success, error
or cancellation. Dynamic clients use an available port on `127.0.0.1`;
pre-registered clients use their configured literal-loopback address and port.
The entire login has a 15-minute deadline.

The CLI prints the authorization URL and instructions to stderr, keeping stdout
for the JSON result. `--no-browser` supports manually opening that URL; its
loopback redirect must reach this AICE process. The interactive transient prompt
also accepts a complete redirect URL in a hidden editor. URLs, codes and tokens
do not enter Session history or model context. Invalid callbacks remain on the
same attempt without exchanging a code. Cancel or denied consent saves nothing;
a dynamic client already registered at the remote server is not deregistered.

A successful login replaces all older OAuth scopes for that service and removes
its connection decision. Inspect status and approve the new fingerprint before
connecting; tool execution still requires its own permission. Login and logout
require the interactive idle reservation and publish saved state by replacing
the MCP owner and clearing MCP Session grants. Logout removes local credentials
and approval, without claiming remote token revocation or signing out another
AICE process.

The supported protocol subset was checked against the
[MCP authorization specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)
and [Linear's documented remote service](https://linear.app/docs/mcp).
It comprises authorization code with S256 PKCE, pre-registered public or
client-secret clients, and dynamic public-client registration. Hosted client ID
metadata documents, private-key authentication, device codes and proprietary SSO
are not implemented. Multiple advertised authorization servers require an
explicit issuer selection.

An explicit unauthenticated GET can obtain a Bearer challenge without saved
cookies or credentials, redirects, or reading an SSE response. Discovery uses
the supplied resource metadata URL or path/root well-known fallback. Authorization
server discovery supports RFC 8414 and the two OIDC path forms. Fallback occurs
only on 404/405; malformed metadata or an identity mismatch fails closed.
Resource identity is canonicalized for scheme/host and a root trailing slash;
issuer identity must match exactly. Explicit configured scopes take precedence,
including an empty set; otherwise challenge scopes apply when present, then the
resource's advertised scopes.

Requests require HTTPS except literal-loopback/localhost HTTP for local services;
HTTPS discovery cannot downgrade to HTTP. Every request and the complete
discovery sequence have a 30-second deadline, JSON bodies are bounded to 1 MiB,
and Bearer challenge text is bounded to 32 KiB. Malformed UTF-8, duplicate
top-level fields (including case aliases), oversized values and unsafe URLs are
rejected. Errors expose status codes and fixed classifications, never remote
bodies, authorization codes, token values or credential-bearing URLs.

Dynamic registration is a separate explicit operation with no retry. Its single
redirect must match a literal-loopback HTTP callback with an explicit valid port
and no query/fragment. Pre-registration supports `none`, `client_secret_basic`
and `client_secret_post` when advertised. Code requests include independent
random state and verifier values, S256 and the exact resource. Callback matching
checks redirect, state, duplicate parameters and issuer when supplied/required.
An accepted callback consumes the attempt atomically before exchange, including
on denial, network failure or cancellation; no code can be exchanged twice.

Refresh credentials bind resource, issuer, token endpoint and client ID.
Rediscovery cannot silently move an existing refresh token to another endpoint.
Token requests include the resource, disable redirects and body replay, and
retain an old refresh token only when the response omits its replacement.
Unknown expiry stays unknown. These primitives neither retry MCP operations nor
grant connection/tool authority. Runtime refresh uses the persisted login
binding; a missing refresh token requires another explicit login.

### OAuth credential state

HTTP definitions accept an optional `oauth` object, which cannot coexist with
an `Authorization` header or a stdio transport. An empty object selects dynamic
public-client registration. Pre-registered clients specify `client_id`, an
explicit literal-loopback `redirect_uri`, and `token_endpoint_auth_method`
(`none` by default, `client_secret_basic`, or `client_secret_post`). Confidential
clients require `client_secret` as an `env`/`auth_ref` reference without a prefix;
literal secrets are rejected. Existing CLI and interactive credential actions
include those referenced slots. `issuer` can select a discovered authorization
server, and optional `scopes` preserves omitted versus explicitly empty lists.
These definitions configure an identity; saving one does not start a login.

OAuth records live under `mcp_oauth` in the existing user auth file, by
source-qualified service and connection-configuration scope. Settings/project
copies of this namespace are ignored with diagnostics. Records bind the canonical
resource, issuer, token endpoint, client ID/method/secret and callback, with
access/refresh tokens, expiry and granted scopes. A fresh login save generates
a random grant ID, replaces that service's older OAuth scopes and removes its
connection decision atomically. The effective fingerprint includes this grant
and immutable binding; access/refresh token rotation, expiry and the returned
scope list do not change that identity. Changing endpoints, configured scopes,
client references or resolved client secrets cannot reuse an old login.

`RefreshMCPOAuth` rereads the latest record under the same auth-file lock used
by existing writers, with a one-minute total deadline. Its caller checks expiry
and performs a bounded exchange while retaining the lock. Concurrent processes
therefore observe a prior rotation; a removed or replaced grant fails before the
callback, and refresh cannot change its immutable identity. Changed records use
the existing atomic writer; unchanged records avoid a rewrite. Cancellation or
an invalid update leaves the original file intact, and actual commit/cleanup
status is retained. Other auth-file edits may wait on this lock and still obey
their existing five-second timeout.

The logout API removes the service's OAuth records and connection decision in
one write while retaining explicit client-secret slots and other services or
providers. Service removal additionally clears those manual slots. Frozen
configuration copies keep secrets private and clone returned records. Transport
values receive only the access-token Bearer header; client/refresh secrets are
not sent to the MCP resource. Result redaction includes all known OAuth secrets.
The connection owner reports missing or expired credentials as `needs_auth`
and checks expiry again at the final dispatch boundary, including under yolo.
Cross-process live revocation remains pending: another running owner keeps its
frozen valid token until expiry; refresh then rejects a removed/replaced grant.

### Automatic refresh before operations

The app serializes each service's operations. After checking connection policy
and acquiring that service gate, it refreshes an expired token before opening,
listing or calling through the transport. Construction, status and ordinary chat
do not refresh. Disabled, denied, missing-credential or unapproved connections
perform no refresh, including under yolo for explicit denies. Unknown expiry
does not trigger speculative refresh.

Refresh rereads the current grant under the existing auth-file lock. A token
already rotated by another process is adopted without another exchange. Otherwise
the app probes/discovers metadata using the saved issuer, verifies the exact
resource/issuer/token-endpoint/client binding, performs one refresh exchange and
saves it before use. There is no consent prompt or client registration. The
one-minute writer limit, protocol request/discovery deadlines and enclosing
operation deadline all apply; the shortest wins. Cancellation and live revoke
cancel the work. A durable rotation remains saved even if later cancellation
prevents publication to that owner.

The HTTP transport reads the current Authorization header through an injected,
local snapshot callback. It performs no OAuth I/O, redirect following or retry.
Rotation preserves the connection, catalog generations, selected schemas and
Session tool grants. The final dispatch check remains local and side-effect-free:
expiry while waiting inside the transport rejects the operation as not dispatched.
A later explicit operation can refresh before entering the queue; the rejected
operation is never replayed.

Refresh failure stops automatic attempts in that owner and reports `needs_auth`.
HTTP 401/403 also stops that OAuth owner without refreshing/replaying the rejected
RPC; a dispatched uncertain outcome remains uncertain. Inspect credentials, then
explicitly reconnect or log in. Reconnect resets the owner and Session grants;
login additionally changes the login identity and removes connection approval.

The owner retains known rotated tokens for result redaction across concurrent
result mapping and later calls. Discovery uses current known credentials when
validating schemas and sanitizing descriptions/resources; result mapping reads
a fresh credential snapshot. Known rotated token literals are redacted before
model/Session output.
The retained list has a 4 MiB/4,096-entry bound, reserving space for two maximum
8 KiB rotated tokens before an exchange; reaching the bound stops refresh and
requires explicit reconnect. It does not evict a known credential while old
results may still be mapped.

## Bound execution permission

Catalog refresh cannot replace an explicit tool deny with ask/allow under the
same binding. Only explicit application policy replacement may change that
upper bound. Managed CUA uses the same Guard with application-generated,
reviewed permissions; its staged integration is described in
[Computer Use](desktop.md#managed-mcp-migration-boundary).

The intrinsic Guard accepts application-published policy snapshots: source,
service ID, effective connection fingerprint, permission scope, enablement and
the current tools' schema fingerprints/eligibility. Publication does not connect
or create user rules. Explicit saved rules are matched by the app before their
decisions enter policy. The run's application-owned mapping supplies each model
name's frozen binding; model arguments, name prefixes, remote annotations and
Session result metadata cannot supply that mapping.

An eligible new tool asks. The application approval bridge offers allow once,
the current tool version for this Session, or the service's current eligible
tool versions for this Session. A service grant snapshots those versions; it
does not authorize later additions. An unchanged catalog refresh preserves
grants. A schema or individual policy change clears the affected tool's grant;
connection, permission-scope or enablement changes clear the service's grants.
The application's permission scope fingerprints include/exclude and restriction
configuration and the effective user-rule revision, so editing filters or rules
invalidates that service's Session grants and stale catalog publications. Changing
back cannot restore a previous Session grant. Removed, disabled, filtered, explicitly
revoked and mismatched bindings deny, including under `--yolo` and the legacy
engine-only Guard-disable switch. Generic unknown-tool name grants do not apply.

Approval captures a transient permit. Before dispatch, the Loop's existing
revalidation boundary checks both the run mapping and current policy. Revocation,
removal/re-addition, identity changes and Session reset invalidate pending
permits. A service-wide approval also rejects a catalog changed while the prompt
was open, without partially granting it. Approval waits and tool execution hold
no Guard lock. This check is not remote transaction isolation: it cannot undo
an operation already dispatched or prove that a server's behavior is unchanged.

Explicit revocation survives ordinary catalog publication. A separate restore
operation removes that deny without restoring grants. `/new` and successful
Session switching use `ResetSessionGrants`, which now also clears MCP grants
and invalidates outstanding permits. These transient approvals are never saved
to Session history or permission configuration; explicit user rules remain. Allow once and yolo do not create Session grants;
noninteractive asks fail closed. Connection authorization remains a separate
application-management responsibility and does not imply tool authorization.

The run catalog publishes tool policy conditionally against its originally
bound configuration. A refresh cannot overwrite a changed connection, scope,
disable, revocation or removal, or lift an existing per-tool denial. `CallChecked` rechecks both the current catalog
version and the Loop's call-local permit after waiting for the connection queue.
This also catches a Session reset between approval and actual dispatch.

These boundaries are exercised together with the real Agent Loop, approval
bridge and a loopback HTTP MCP server, including application startup. Management
CLI commands save connection decisions and test fresh connections; interactive
controls additionally publish saved changes and revoke live service capabilities.

## Run catalog and tool search

`app.mcpCatalog` borrows source-bound lazy connection views and freezes
configuration/connection membership for one run (at most 129 services, including
the reserved managed-service capacity). The application owner admits at most
eight actual connections, including in-progress initialization. Construction
does not connect or discover tools. `tool_search` can query keywords, browse a
source-qualified service, select
up to five exact IDs, and continue a result page using `next_offset`. The query
matches words in names and descriptions with common English function words
removed, limited plural/`-ion` normalization, and overlapping Han character pairs
for Chinese phrases. Distinct query terms receive inverse catalog-frequency
weights; repetition and name placement do not increase a term's weight. Query
terms are accumulated in a fixed order and equal scores use stable ID ordering.
A small fixed action vocabulary also matches add/make/create, find/locate/search,
show/get/read and remove/delete. Each group contributes once; update, append,
archive and cancel remain distinct. These are discovery hints, not authorization.
A query equal to a complete remote tool name (ignoring case and outer spaces)
ranks that tool before prose matches; same-name tools still use stable ID order.
This is lexical retrieval with those fixed groups, without translation or
general semantic inference. Search guidance asks the model to retain original-language
keywords when adding translations and refine or browse when candidates do not
match the requested operation. This is guidance, not an execution guarantee.
Misses can be recovered by browsing or exact selection. Changed catalogs may
move page boundaries. The offline retrieval corpus under `internal/app/testdata`
keeps language misses separate from exact-ID correctness; it does not prove
actual-model task quality.

Discovery has a ten-second overall deadline and up to four concurrent service
requests. One slow service does not serialize all others. Disabled, revoked,
removed or rebound services are checked before discovery. Unconnected services
report an unknown catalog rather than an empty one; partial, failed or invalidated
discovery removes that run's entries for the affected service and reports an
incomplete result. Each service retains the client limits of 2,000 entries and
4 MiB; a run additionally bounds catalog data to 32 MiB, eligible tool
entries to 16,000, and retained model-name identities to 32,000.

Stable IDs combine the source-qualified configured service key and escaped
original tool name. Model names contain a short readable prefix and an identity
hash, stay under 64 ASCII characters, and are checked for collisions. Each
immutable revision includes connection identity, exact schemas, descriptor and
catalog generation. `list_changed` invalidates selected versions before dispatch;
refreshing creates new executable versions. The Loop alone owns selection:
search proposes typed references, which become definitions only after recording
the complete tool-result group. Neither search text nor Session replay selects
or authorizes a tool. Closing a run invalidates its bindings without closing a
borrowed connection.

Each main run binds a new catalog through an app-owned context capability used
by discovery tools and the Guard. It starts with builtins, `tool_search`,
`mcp_resource_list` and `mcp_server_info` when MCP is configured, and at most
4 KiB of service IDs/status
summaries. Required services connect before the first model request; pinned
tools additionally require successful tool discovery. Both share a ten-second
preparation deadline. Resource-only services can be required without exposing
tools. Unavailable/denied services or missing pins fail preparation. The Loop
applies its existing complete-schema budget to pins. Optional services do no I/O
until explicit discovery. Transport reuse across interactive runs does not retain
selected definitions; `/btw` remains tool-free. Remote server instructions never
enter the system prompt; only trusted usage guidance for the discovery builtins
appears there.

## Server usage instructions

`tool_search` includes source-tagged usage previews for services represented by
its selected candidates. These come only from already initialized connections;
previews cause no extra connection or RPC. Each excerpt is at most 512 UTF-8
bytes, with at most five services and an 8 KiB combined JSON budget. The whole
search result retains its existing 32 KiB bound. An omitted preview can be read
explicitly with `mcp_server_info`.

`mcp_server_info` takes an exact source-qualified `service`, optional `offset`,
`length` and `revision`. It initializes only that service after connection
approval, under the ten-second discovery deadline, and reuses the client's
retained initialization data. It does not list/call tools, read resources or
fetch links. Disabled, denied, revoked or closed-run bindings cannot initialize
or disclose a current server-info snapshot. Construction and ordinary chat remain
inert; reading metadata does not grant any remote operation.

The response has source location, connection fingerprint, server name/version,
negotiated protocol and advertised capabilities, followed by a separate text
block containing the instruction page. The first page defaults to 1,024 bytes;
`length` accepts up to 8,192 bytes. Continue with the returned `next_offset` and
`revision`. Offsets count redacted UTF-8 bytes; split-character offsets and
lengths too small for the next character fail explicitly. A content/source
revision mismatch requires restarting at offset zero, preventing mixed pages
after reconnect or a changed redaction view. Empty instructions are a complete
empty page, not a missing-capability error.

Instructions are retained within the client's initialization-message limit and
the view's 16 MiB raw/redacted text bound. Known credentials are redacted before
paging, including values crossing a page boundary. Oversized or invalid text
fails explicitly. Display name/version/protocol are limited to 256/128/64 bytes
with `metadata_clipped`; page metadata has a 32 KiB ceiling. Read pages and
search previews become ordinary tool results in the existing Session; there is
no second instruction store. Unread text remains connection data, so a different
initialization may produce a different revision after reconnect.

Every preview/page labels remote text as untrusted usage data. It cannot override
user instructions, project trust or Guard decisions. Advertised MCP Prompts are
shown but remain unsupported; remote Skills are neither installed nor activated.
Existing local Skill discovery, trust and explicit loading rules are unchanged.

## Resource discovery and reading

`mcp_resource_list` takes an exact source-qualified `service`, with optional
`offset` and `limit` (default/maximum five). It opens only that service after
connection authorization, lists its resource directory under the normal client
bounds and a ten-second discovery deadline, and returns metadata with a
`next_offset` cursor. It does not read resources, follow links or call tools.
Unsupported resources, unknown service IDs and unavailable/incomplete catalogs
produce explicit notices; they are not presented as an empty successful catalog.
Descriptions and titles are bounded untrusted data, known credentials are
redacted, and credential-bearing URIs are omitted with notice. Each response
is bounded to 32 KiB. Repeated pages rediscover the directory, so a changed
catalog may move page boundaries.

Successful discovery proposes one service-bound `mcp_resource_read_<hash>`
definition, installed only after the complete result group is recorded. It uses
the same Loop schema budget, immutable revisions and Guard path as selected MCP
tools. Its stable catalog identity is `<service>/resource/read`; ordinary
`tool_search` remains a search of the server's tool catalog. The reader accepts
one exact `uri`, at most 4,096 UTF-8 bytes, including a URI supplied by a resource
link rather than the directory. The URI is sent only to that bound MCP server;
AICE never interprets it as a local path or an HTTP fetch target. Templates,
subscriptions and MCP Prompts are not exposed in this version.

Connection approval and listing confer no read authority. Resource reads ask
separately, including for services claiming read-only behavior. The existing
allow-once/Session choices apply: a resource-reader Session grant covers any URI
on that service, and a service grant covers only operations discovered at approval
time. This scope appears in the approval reason. Service disable, deny, revoke,
identity changes and Session reset still win over yolo and saved grants. Ordinary
tool include/exclude filters apply to tools, not resource URIs; disable or revoke
the service to prevent its resource access. There is no URI-specific persistent
permission setting in this version.

Binding metadata adds optional `operation: "resources/read"`; an absent operation
retains the legacy ordinary-tool identity. Guard separates operation domains, so
a remote tool also named `resources/read` cannot inherit resource authorization.
Refreshing the tool catalog preserves the resource policy and vice versa; service
revocation invalidates both. Resource and tool list notifications invalidate their
own run references independently. The connection queue rechecks the current
binding and call-local permit immediately before dispatch. Reads never reconnect
or automatically replay on failure, cancellation or unknown outcomes.

The ordinary MCP result mapper records ordered text/images, resource provenance,
explicit storage loss and execution state in the append-only Session. Model view
budgets and `tool_result_read` apply unchanged; returned links are not expanded.
A successful directory lookup does not establish that a later read is safe or
will succeed.

## Connection ownership

`app.mcpOwner` owns the configured transports for one print invocation or
interactive application. Read-only status and constructing a run never connect
optional services. Status distinguishes `disconnected`, `connecting`, `ready`,
`needs_approval`, `needs_auth`, `failed`, and `disabled`. Discovery checks current
Guard policy, configuration restrictions and the exact saved connection decision
before opening. `--yolo` bypasses Ask only; explicit connection denies and
disabled/revoked services remain denied. Known missing credentials do not reach
the transport. Status errors contain application-owned descriptions, not remote
error bodies or credential-bearing URLs.

The owner serializes each service without holding its bookkeeping lock during
I/O. Its queue is included in the configured call timeout (60 seconds by
default); services have independent queues. Concurrent searches reuse the same
connection. Failed initialization releases its slot and can be retried by a
later discovery; tool dispatch never initializes or reconnects a service.
Repairing an already established failed transport currently requires replacing
the owner. `aice mcp reconnect` tests a fresh connection in a separate invocation;
interactive reconnect publishes a new owner while idle (currently resetting all MCP connections).

Revocation cancels the service context, denies its Guard binding and invalidates
borrowed versions before closing the owned connection. Closing the application
cancels and joins work, including closing a client returned after cancellation.
Only owned transports/children are closed. Already dispatched operations retain
the client's returned/unknown outcome; cancellation never replays a tool action.

`mcpclient.Open` accepts one explicitly supplied stdio or Streamable HTTP
connection, initializes it, and returns server information. It does not load
configuration, discover tools eagerly, install programs, or grant permission.
The caller owns `Close`. SDK types remain private to this package; the current
SDK version is unchanged at `github.com/modelcontextprotocol/go-sdk v1.6.1`.

Stdio requires an absolute executable and explicit absolute working directory.
Arguments go directly to the process without shell expansion. Only a small
base environment allowlist and caller-supplied variables are inherited; AICE's
provider credentials are not automatically copied. Stderr is discarded rather
than retained as potentially sensitive diagnostics. Closing stops only the
owned child, closes both pipes, and reaps it with bounded waits. It does not
signal a process group or stop a separate shared daemon. Canceling an active
stdio operation closes this connection, including when a child stops reading
stdin. A caller must explicitly establish a new connection afterward.

HTTP accepts HTTPS or local loopback HTTP. Credentials must be supplied by
the caller as headers; URL userinfo is rejected. Redirects, request-body
replay, idempotency headers, SDK SSE reconnection, and the SDK's automatic
OAuth authorization/replay path are disabled. HTTP status errors expose the
status code without the endpoint, headers, underlying error text, or response
body. HTTP session cleanup attempts a DELETE bounded to one second to terminate
the owned protocol session, not the server process. A transport failure during
remote close returns `ErrTransport`; local lifetime cancellation and idle-pool
cleanup still run.
Authentication acquisition, refresh, credential storage and connection approval
are not implemented here. The loopback
`TestHTTPHungCleanupClosesLocalConnection` verifies that a stalled DELETE returns
`ErrTransport`, cancels the peer request and closes local sockets before fixture
teardown. Repeated close retains the error without another DELETE; subsequent
tool calls are rejected before dispatch. This does not identify a remote
service failure or prove that its server-side session was removed.

Internal callers may pin `Config.ProtocolVersion` to an exact SDK-supported tier;
unsupported pins fail before connecting and a differing server tier is rejected.
An empty pin retains normal SDK negotiation. Cua pins only its own connection
to `2025-06-18`; ordinary services still use the SDK default. This is not a new
user configuration field. `StdioConfig.ReplaceEnvironment` lets reviewed native
consumers supply their complete environment instead of augmenting the base
allowlist. An empty complete environment does not inherit host variables
(except runtime-required OS variables added by Go itself).

The client advertises no roots, sampling, or elicitation capability. Server
instructions and tool annotations are untrusted data and confer no authority.
Prompts are reported as an advertised server capability but are not exposed
as callable client operations. Streamable HTTP retains the SDK's initialization
hooks, negotiated protocol header and standalone notification stream.

## Discovery and results

`Tools` and `Resources` paginate explicitly. Each operation returns values
owned by its caller, a generation, completeness and an explanatory notice.
`list_changed` invalidates the generation before the notification frame is
forwarded to the SDK. Pagination interrupted by a change is incomplete;
consumers must recheck generations before dispatch. Invalid entries, duplicate
identities, repeated cursors and page failures are reported, not treated as
an empty catalog. Schema validation here checks JSON object shape; the tool
adapter is responsible for the effective model schema and argument contract.

The defaults are 16 MiB per incoming JSON message or SSE event,
4 MiB of catalog pages, 2,000 catalog entries and 20 pages. Internal callers may
set the message limit up to 24 MiB (used by Cua); the other defaults are hard
ceilings. Callers may lower any limit. Frames are bounded before SDK decoding. Arguments are limited
to a 1 MiB JSON object. The default initialization timeout is 15 seconds and
the operation timeout is 60 seconds, including time queued behind another
operation on that connection. Different connections have independent queues.
HTTP additionally bounds dialing/TLS, response headers and cleanup.

`Call` dispatches once. `ReadResource` explicitly reads one URI on that server;
returned links are never fetched automatically. Both return execution state:
local validation/cancellation before dispatch is `not_dispatched`; an observed
result or protocol error is `returned`; a transport failure after a write may
have started is conservatively `unknown`. Timeouts, cancellation, `isError`,
401 responses, redirects and disconnection never replay the operation. Result
storage limits produce an explicit loss notice.

Bounded raw response frames preserve schema and structured-result numbers
before the pinned SDK can convert them to floating point. Content blocks stay
in their received order, including resource links and unfamiliar kinds.
Protocol content tags are decoded into neutral block values here; unfamiliar
or malformed blocks retain their original JSON with an unsupported tag.
These transient values are not persisted separately. `tool.MCP` maps text,
validated images, embedded text/images and labeled resource links in source
order into ordinary tool results. Shared media processing preserves image
originals and coordinate mappings. Structured JSON retains its original bytes
and exact numbers unless known credentials require redaction. Session encoding
uses the [structured source companion](contracts.md#structured-tool-outcomes)
when necessary to preserve whitespace and escapes through JSONL recording;
this keeps live and replayed model projections identical. `isError`, partial
content, bound provenance and returned/unknown/not-dispatched states survive
Session recording and reopening. Loop refusals before starting a bound tool also
retain provenance and `not_dispatched`; this does not infer an outcome for legacy
tools that throw an error after starting.

Storage ceilings are 256 source blocks, 1 MiB aggregate text, 1 MiB structured
JSON, and 16 MiB aggregate image views plus originals. Excess, malformed images,
unsupported audio/binary payloads and unfamiliar content payloads receive explicit
loss notices rather than silent omission or a claim of recoverability. Media
preparation has a separate five-second local deadline so cancellation after a
returned RPC does not erase received text or structured facts. It never retries
the operation. Backend errors are replaced with a generic diagnostic; returned
server text remains available after literal credential redaction.

The connection owner supplies known credential values explicitly. Redaction
covers plain text, resource metadata and decoded JSON strings/numbers, including
earlier duplicate object fields. Invalid/deep JSON or redaction key collisions
are omitted with loss; schemas containing a known credential are rejected whole.
Encoded image data containing a literal known value is omitted. This is not a
general detector for secrets drawn into pixels or arbitrary obfuscation. Source
redaction/loss differs from context clipping. No side transcript or additional
database is created.

## Model views and result readback

The Loop records the source result before constructing the next model request.
Only its derived request view is clipped: each result carrying `details` gets
10% of the model context window, capped at 4,096 estimated tokens with a
256-token minimum; an unknown window uses 4,096. These are local estimates,
not a provider tokenizer guarantee. Legacy results keep their existing tool
limits. Total context still follows ordinary safe-boundary compaction.

The view preserves execution state, error status and source block order. Text
prefixes end on UTF-8 boundaries; structured JSON is included whole or omitted.
Images consume the existing 1,200-token estimate and require at most 4 MiB of
view bytes. A clipping notice directs the model to readback; long source-loss
notices are abbreviated only in this view. Neither clipping nor readback changes
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
precision. Text uses UTF-8 byte offsets and lengths up to 8,192 bytes, with
`next_offset` for continuation. Metadata is capped at 32 KiB and a selected
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

Deterministic tests use temporary settings, synthetic credentials, re-executed
local test processes and loopback HTTP peers. They do not establish external
account interoperability, actual-model task quality or native desktop behavior.
The current regression boundaries are:

| Boundary | Evidence |
| --- | --- |
| Configuration and identity | Source/endpoint/credential isolation, frozen environment, trust and deny composition, empty includes, scoped atomic patches, concurrent writers, malformed-file preservation and reserved managed identities in `internal/config` |
| Guard and run selection | Identity/schema-bound grants, once/Session/user-rule distinctions, new/reverted schemas, revoke/remove/re-add, approval-time changes, safe selection boundaries, request budgets and concurrent Run isolation in `internal/guard`, `internal/agent` and the [app Guard/Loop tests](../internal/app/mcp_guard_test.go) |
| Transport and ownership | JSON/SSE and stdio, finite pagination/frames, exact numbers, ordered blocks, independent invalidation, environment isolation, normal/forced/blocked-pipe cleanup, cancellation, redirect isolation and no replay after failure in `internal/mcpclient`; lazy connection, queue revalidation, slot limits and late-client cleanup in [owner tests](../internal/app/mcp_owner_test.go) |
| Actual commands and interactive management | Production Cobra/Print and Bubble Tea tests exercise add/replace, approval, discovery, reconnect, partial saves, cancellation, stale panels, runtime deny, permissions, inert managed status/navigation and loaded-tool lifetime; configuration, models and external peers are test-owned |
| OAuth and refresh | Protocol discovery/PKCE/callback/rotation checks in `internal/mcpauth`; [four-process single-refresh locking](../internal/config/mcp_oauth_test.go); [actual CLI/TUI login/logout](../internal/app/mcp_oauth_login_test.go) and [automatic refresh with durable redaction](../internal/app/mcp_oauth_refresh_test.go), including preserved connection/grants, expiry in the queue, denied work, revoked grants, 401 without replay and bounded credential history |
| Results and recovery | Three API adapters preserve ordered projections; LLM/Session tests cover exact structured source spelling, validation, legacy records, cloning, branches and compaction. [Actual Print readback](../internal/app/result_read_test.go) checks bounded views, exact source retrieval and one remote call with and without persistent Session storage |
| Resources and server instructions | [Resource tests](../internal/app/mcp_resources_test.go) separate resource/tool authority and invalidation and preserve ordered results; [instruction tests](../internal/app/mcp_info_test.go) check bounded source-tagged previews, UTF-8 paging, cross-page redaction, revisions and no promotion into system instructions or execution authority |
| Permanent permissions | [CLI/Print/Settings tests](../internal/app/mcp_permissions_test.go) verify cross-Session explicit rules, schema changes asking again, deny under yolo, independent connection approval and invalidation of old references/permits |
| Managed CUA | Synthetic catalog/Guard tests verify application-owned identity, reviewed mode/schema constraints, final dispatch revalidation and no second production entry. Native evidence and archived comparison limits belong to [Computer Use](desktop.md#managed-mcp-migration-boundary) |

### Platform coverage

The implementation has passed the full macOS unit, vet and race checks and a
Linux arm64 deterministic MCP package subset. Windows cross-compilation does
not establish native execution. Reproduce checks through
[Verification](collaboration.md); native desktop limits belong to
[Computer Use](desktop.md#platform-evidence).

### Real configured service interoperability

On macOS, the configured Print path has read from the official Filesystem
stdio server and public DeepWiki HTTP server. Both checks use temporary
configuration, explicit connection and read permissions, a scripted model and
Session reopening. They exercise discovery followed by one authorized call,
not actual-model reasoning or performance.

The offline `TestMCPPrintInteropHarness` and `TestMCPInteropModelSelection`
verify this path without external services. To repeat the real-service checks,
use reviewed installed paths; the tests never install packages:

```sh
AICE_MCP_FILESYSTEM_TEST=1 \
  AICE_MCP_TEST_NODE=/absolute/path/to/node \
  AICE_MCP_FILESYSTEM_ENTRY=/absolute/path/to/server-filesystem/dist/index.js \
  go test -race -tags=integration ./internal/app \
  -run '^TestMCPFilesystemPrintInterop$' -count=1 -timeout=120s -v

AICE_MCP_DEEPWIKI_TEST=1 go test -race -tags=integration ./internal/app \
  -run '^TestMCPDeepWikiPrintInterop$' -count=1 -timeout=150s -v
```

A later DeepWiki timing run returned a close error after successful reads.
Local regression tests prove bounded request/socket cleanup and no replay;
they cannot prove remote session deletion. No general latency advantage is claimed.

### External OAuth and remaining gates

Linear's read-only browser login, two current-user reads around one refresh,
persisted credential rotation and same-connection reuse passed on macOS.
The opt-in `TestLinearOAuthReadRefresh` advances only the validation service's
local expiry; this does not verify natural expiry, all providers or platforms.
`TestMCPExternalOAuthHarness` checks the same orchestration with a local peer.
Account content and credentials must not be written to retained reports.

The synthetic model comparison found lower discovery token use but also wrong
capability selection and a copied-result error; it does not establish universal
quality or performance improvement. Keep generated model runs outside the
repository. Native CUA migration evidence and outstanding platform limits are
maintained in [Computer Use](desktop.md#platform-evidence).
