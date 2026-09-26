# Reviewed Cua schemas

`macos-0.29.1.json` contains the unmodified `input_schema` objects for the 15
native tools used by AICE. They were extracted from the signed macOS universal
Cua Driver **0.29.1** binary with `dump-docs --type mcp` on 2026-09-26, using an
isolated HOME and disabled telemetry/update checks. This finite metadata command
does not start a desktop runtime, enumerate windows, capture or request grants.

Upstream: <https://github.com/trycua/cua>, commit
`7a8f66ad04e62fccb18cca9965f2964fcaee124e`, release `cua-driver-rs-v0.29.1`.
The artifact, signing and licensing evidence is in
[VENDOR.md](../../deps/cua/VENDOR.md). These upstream schema definitions are
MIT licensed; the preserved [LICENSE](../../deps/cua/LICENSE) applies.

The CLI inventory and MCP `tools/list` use the SDK's canonical tool inventory.
At connection time AICE compares complete parsed JSON objects, ignoring only
object-key order and whitespace. Changes to required fields, types, enums,
defaults, bounds, target alternatives, descriptions or other members require
review, even when a change might be backward compatible. Extra upstream tool
names are ignored and remain unavailable to the private client.

This strict check is intentional for each fixed platform artifact. A version or
platform change must update its schema pin and typed adapter together; do not
regenerate pins merely to make a failing connection test pass. Schema matching
is evidence of the advertised contract, not native input/capture acceptance.

`TestNativeCuaSchemaInventory` repeats the metadata-only comparison against an
explicitly supplied verified binary. Default tests mutate the reviewed fixture
and exercise rejection through the real MCP handshake before any `tools/call`.

`linux-status-0.29.1.json` pins the two read-only tools used by Linux service
inspection: `get_config` and `check_permissions`. They were exported from the
checksum-verified arm64 binary in an isolated Debian 13 container on 2026-09-26.
Linux has no `prompt` permission argument. This inspection client admits only
those two tools; it cannot dispatch input or capture even though the service
advertises additional tools. The native headless inspection test verifies this connection
path without declaring the desktop usable.

`linux-0.29.1.json` contains the unmodified schemas of all 15 tools from that
same native Linux metadata export, under the same upstream MIT license. The
Linux runtime uses this inventory for both owned stdio and verified shared
service connections. Eleven schemas differ from macOS. Linux's typed adapter
also accounts for empty permission arguments, discovered XDG launch commands,
capture validity and actionable-only semantic projections. The opt-in X11 probe
and Manager acceptance test compare this production pin during the real MCP
handshake. Matching schemas do not establish Wayland or foreground safety;
those routes remain unavailable pending their own reviewed adapters.
