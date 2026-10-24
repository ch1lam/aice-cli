# Cua Driver provenance

The native helper is pinned to **0.38.0**, release
[`cua-driver-rs-v0.38.0`](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.38.0),
source commit `85e2b1318bd77ccf3cfa88ccdf875a4bb7eaed19`. GitHub marks every
`cua-driver-rs` release as a pre-release so the repository-wide latest pointer
stays on another product; 0.38.0 is the version Cua's own `check-update`
reports as latest. The helper is MIT licensed; preserve LICENSE with
installations. AICE does not embed its Node/Python SDK, perception extensions
or browser-profile tools.

The adjacent upstream release manifest has SHA-256
`d25e212faf86143f96ad77da7f9145c75b54ffd1164675077a30a5a8f2af59be`.
On 2026-10-25 the five full distribution archives (macOS universal and
Windows/Linux on amd64/arm64) were downloaded and hashed; the digests agree
with `SHA256SUMS`, the release manifest and GitHub's asset digests, and with
the pins in `cua.go`. The Linux/Windows executable bytes inside those archives
supply the per-file hashes in `cua_install_native.go`; the installed file set
is unchanged from earlier pins and the archive LICENSE matches the embedded one.

The extracted macOS App passed `codesign --verify --deep --strict` with the Cua
signing requirement (bundle `com.trycua.driver`, Developer ID team
`YCK386LBJ7`, Cua AI, Inc.) and `spctl --assess --type execute`, and reports
`cua-driver 0.38.0`. Do not bypass signature failures by re-signing or
removing quarantine. A new installation must report the pinned version; an
existing Cua-signed `/Applications/CuaDriver.app` of another version is reused
with a warning, because Computer Use talks to it only through MCP and Cua's
own updater may manage that shared App.

No upstream installer is executed. The published top-level install.sh delegates
to a secondary installer and includes skill/PATH integration outside AICE's
scope. Windows installation also has an autostart choice. Managed installation
must extract the exact pinned artifact, preserve the signed App, avoid PATH,
autostart and agent-skill modifications, and validate before publication.

Cua sends content-free product telemetry by default. AICE-owned child processes
set `CUA_DRIVER_RS_TELEMETRY_ENABLED=false` and disable the child's update check
with `CUA_DRIVER_RS_UPDATE_CHECK=false`; this does not modify preferences of
an existing shared service. Cua updates are explicit; no `latest` resolution or
upstream update command belongs in the runtime path.

The Go client uses the official `github.com/modelcontextprotocol/go-sdk`,
with its version locked in [go.mod](../../../go.mod). Its LICENSE
contains the Apache-2.0/MIT transition notice; both licenses must be retained
when redistributing SDK source. The SDK owns discovery/initialize, request
correlation, cancellation, subscriptions, pagination and content decoding.
The Driver is an ordinary MCP service using the SDK's default negotiation. The
SDK stays private to `internal/mcpclient`; installation stays in
`internal/deps` and the service definition in `internal/desktop`.

SDK transitive runtime additions: google/jsonschema-go (MIT),
segmentio/encoding and segmentio/asm (MIT), yosida95/uritemplate/v3 (BSD-3-Clause).
Versions are locked in go.mod/go.sum. Existing OAuth/sys dependencies retain
the application's newer versions. Tests use a raw local peer, not paid models
or the user's desktop.
