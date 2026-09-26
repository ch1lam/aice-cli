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

This strict check is intentional for the fixed macOS artifact. A version or
platform change must update its schema pin and typed adapter together; do not
regenerate pins merely to make a failing connection test pass. Schema matching
is evidence of the advertised contract, not native input/capture acceptance.

`TestNativeCuaSchemaInventory` repeats the metadata-only comparison against an
explicitly supplied verified binary. Default tests mutate the reviewed fixture
and exercise rejection through the real MCP handshake before any `tools/call`.
