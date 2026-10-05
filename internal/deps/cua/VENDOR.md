# Cua Driver provenance

The native helper is pinned to **0.30.4**, release
[`cua-driver-rs-v0.30.4`](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.30.4),
source commit `bf6c76786d938070f4ecf1e44004752f69f518b8`.
The helper is MIT licensed; preserve LICENSE with installations. AICE does
not embed its Node/Python SDK, perception extensions, or browser-profile tools.

The adjacent upstream release manifest has SHA-256
`df3cdde0a4b2c1260abe548ffe3b6ca6967935d131b084e9a826b815b02fe59c`.
It and GitHub's release asset digests agree with the pins in `cua.go`.
All five full distribution archives (macOS universal and Windows/Linux on
amd64/arm64) were downloaded and independently hashed on 2026-09-30. The
selected Linux/Windows executable bytes supply the per-file hashes in
`cua_install_native.go`; no unverified executable digest is carried forward.

The staged macOS App passed `codesign --verify --deep --strict` with the Cua
signing requirement and `spctl --assess --type execute`. Its signed identity
remains bundle `com.trycua.driver`, Developer ID team `YCK386LBJ7` (Cua AI, Inc.).
The same verified App was installed at `/Applications/CuaDriver.app` on
2026-09-30 and reports 0.30.4. The previous App and local AICE binary were
preserved outside the installation before replacement. Do not bypass signature
failures by re-signing or removing quarantine.

The 15 macOS schemas were exported by the verified signed binary using
`dump-docs --type mcp` with an isolated home and telemetry/update checks disabled.
The Linux arm64 binary exported the same finite metadata inventory as an
unprivileged user in a disposable Debian container with libX11, libXi and
libxkbcommon installed. These commands do not access a desktop. The two Windows
status schemas were reviewed in the fixed source; Windows binaries were not
executed and native Authenticode trust was not tested on this host.
The updated Linux arm64 private installer also passed its native install and
read-only reuse test in that container. See
[schema provenance and review](../../desktop/schema/README.md).

Version 0.30.4 changes macOS foreground pixel clicks to HID delivery that moves
the physical pointer and leaves it at the target; it also fixes per-display
foreground-window verification and cursor-theme hotspot transforms. The
main-screen-only macOS cursor overlay limitation remains. Archive integrity,
metadata exports and schema review do not establish native input, focus, cursor
alignment or real-application acceptance. Earlier 0.29.1 native results remain
versioned evidence in [Computer Use](../../../docs/desktop.md#platform-evidence).

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
AICE supplies `2025-06-18` through `ClientSessionOptions.ProtocolVersion` for
the current Driver and checks the negotiated version and Driver identity;
ordinary MCP connections use the SDK's default negotiation. The SDK stays private
to `internal/mcpclient`, shared by ordinary MCP services and the desktop
connection. Native installation and runtime admission remain in `internal/desktop`.

SDK transitive runtime additions: google/jsonschema-go (MIT),
segmentio/encoding and segmentio/asm (MIT), yosida95/uritemplate/v3 (BSD-3-Clause).
Versions are locked in go.mod/go.sum. Existing OAuth/sys dependencies retain
the application's newer versions. Tests use a raw local peer, not paid models
or the user's desktop.
