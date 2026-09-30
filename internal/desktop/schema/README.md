# Reviewed Cua schemas

`macos-0.30.4.json` contains the unmodified `input_schema` objects for the 15
native tools used by AICE. They were extracted from the signed macOS universal
Cua Driver **0.30.4** binary with `dump-docs --type mcp` on 2026-09-30, using an
isolated HOME and disabled telemetry/update checks. This finite metadata command
does not start a desktop runtime, enumerate windows, capture or request grants.

Upstream: <https://github.com/trycua/cua>, commit
`bf6c76786d938070f4ecf1e44004752f69f518b8`, release `cua-driver-rs-v0.30.4`.
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
platform change must review its schema pin and native constraints together; do not
regenerate pins merely to make a failing connection test pass. Schema matching
is evidence of the advertised contract, not native input/capture acceptance.

`TestNativeCuaSchemaInventory` repeats the metadata-only comparison against an
explicitly supplied verified binary. Default tests mutate the reviewed fixture
and exercise rejection through the real MCP handshake before any `tools/call`.

`linux-status-0.30.4.json` pins the two read-only tools used by Linux service
inspection: `get_config` and `check_permissions`. They were exported from the
checksum-verified arm64 binary in an isolated Debian 13 container on 2026-09-30.
Linux has no `prompt` permission argument. This inspection client admits only
those two tools; it cannot dispatch input or capture even though the service
advertises additional tools. The native headless inspection test verifies this connection
path without declaring the desktop usable.

`linux-0.30.4.json` contains the unmodified schemas of all 15 tools from that
same native Linux metadata export, under the same upstream MIT license. The
Linux runtime uses this inventory for both owned stdio and verified shared
service connections. Eleven schemas differ from macOS. Linux's native boundary
also accounts for empty permission arguments, discovered XDG launch commands,
capture validity and actionable-only semantic projections. The opt-in X11 probe
and Manager acceptance test compare this production pin during the real MCP
handshake. Matching schemas do not establish Wayland or foreground safety;
those routes remain unavailable pending their own reviewed adapters.

`windows-status-0.30.4.json` pins only `get_config` and `check_permissions`.
Unlike the macOS/Linux inventories above, this pin was reviewed from
[`platform-windows/src/tools/impl_.rs`](https://github.com/trycua/cua/blob/bf6c76786d938070f4ecf1e44004752f69f518b8/libs/cua-driver/rust/crates/platform-windows/src/tools/impl_.rs)
at the same fixed source commit, not exported from an executing Windows binary.
Both real Windows definitions have empty properties and reject extra arguments;
the non-Windows build stubs are not their contract. The separate inspection client
admits only these two tools and compares the advertised schemas before any call.
`TestNativeWindowsServiceInspection` is prepared to verify that pin through an
installed native service, but has not run here. This source review and synthetic
handshake coverage are not Windows runtime acceptance. The same MIT provenance
and preserved license apply.

## Upgrade review

Compared with 0.29.1, the admitted macOS/Linux schemas remove nonstandard
`uint32`/`uint64` format annotations from nested target IDs, retaining integer
types, minimum bounds and required fields. macOS `scroll.amount` additionally
clarifies that oversized requests are clamped. The two Windows status schemas
and Linux status schemas are unchanged. No tool was added to AICE's inventory,
no host policy was relaxed, and full schema equality remains required.

Input behavior was reviewed separately: macOS foreground pixel clicks now
use exact-window HID activation and leave the physical pointer at the target.
The builtin guidance describes this behavior. Source review and metadata
comparison do not certify physical focus or cursor alignment.
