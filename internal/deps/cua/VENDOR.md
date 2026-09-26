# Cua Driver provenance

The native helper is pinned to **0.29.1**, release
[`cua-driver-rs-v0.29.1`](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.29.1),
source commit `7a8f66ad04e62fccb18cca9965f2964fcaee124e`.
The helper is MIT licensed; preserve LICENSE with installations. AICE does
not embed its Node/Python SDK, perception extensions, or browser-profile tools.

The adjacent upstream release manifest has SHA-256
`d114a50c1487ad20c7f7ca6fb6380284f160fd967d5e2f2050ccbb444e72b52b`.
It and GitHub's release asset digests agree with the pins in `cua.go`.
The macOS universal archive was downloaded and its bytes independently hashed.
Windows/Linux assets have published digests only; they have not been executed
or locally hashed. Artifact pinning does not establish native acceptance.

The downloaded macOS App passed `codesign --verify --deep --strict` and
`spctl --assess --type execute` on 2026-09-26. Signing identity:
`Developer ID Application: Cua AI, Inc. (YCK386LBJ7)`;
bundle `com.trycua.driver`; stapled notarization ticket. Sandboxed codesign
reported an invalid signature with unavailable authority; the same unchanged
bytes passed verification with access to system certificates. Do not work
around a production signature failure by re-signing or removing quarantine.

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

The Go client is `github.com/modelcontextprotocol/go-sdk v1.6.1`, the official
SDK maintained under the Model Context Protocol organization. Its LICENSE
contains the Apache-2.0/MIT transition notice; both licenses must be retained
when redistributing SDK source. The release implements legacy initialize,
request correlation, cancellation, pagination, and content decoding. v1.7.0
introduces modern discovery by default; it was reviewed but is not selected for
this legacy-only connection. AICE pins initialize to `2025-06-18` and checks the
negotiated version and Driver identity. The alternative of implementing a new
JSON-RPC client would duplicate protocol/lifecycle code; the SDK stays private
to `internal/desktop`, without creating a generic MCP plugin platform.

SDK transitive runtime additions: google/jsonschema-go (MIT),
segmentio/encoding and segmentio/asm (MIT), yosida95/uritemplate/v3 (BSD-3-Clause).
Versions are locked in go.mod/go.sum. Existing OAuth/sys dependencies retain
the application's newer versions. Tests use a raw local peer, not paid models
or the user's desktop.
