# Computer Use

Computer Use is the Cua Driver running as an ordinary MCP service, `user:cua`.
AICE installs the pinned Driver, runs Cua's public macOS permission flow and
writes the service definition; everything after that is the generic
[MCP](mcp.md) path: connection approval, catalog loading, Guard tool approval,
results, Session retention and readback. There is no managed service, reserved
ID, control-mode setting or AICE-side desktop lock.

Cua owns its runtime, permission mode, sessions, input delivery, targeting and
concurrency. Its MCP server instructions and tool descriptions carry its usage
rules and reach the model like any other service's; AICE ships no separate
Computer Use guide.

## Set up and disable

Use `/desktop` or Settings → Tools & Network → Computer Use.

**Set up / Repair** shows a disclosure, then:

1. installs or reuses the pinned Cua Driver (see [Installation](installation.md#computer-use-helper));
2. on macOS, runs `cua-driver permissions grant`, which launches CuaDriver through
   LaunchServices so Accessibility and Screen Recording are granted to the
   CuaDriver app and checks live capture (four-minute deadline; cancelling
   cannot undo completed grants or close system dialogs);
3. adds, replaces or re-enables the user service `cua`:

   ```json
   {"name": "Computer Use (Cua Driver)", "transport": "stdio",
    "command": "/Applications/CuaDriver.app/Contents/MacOS/cua-driver", "args": ["mcp"],
    "env": {"CUA_DRIVER_RS_TELEMETRY_ENABLED": {"value": "false"},
            "CUA_DRIVER_RS_UPDATE_CHECK": {"value": "false"}}}
   ```

   On Linux and Windows the command is the private installation under
   `~/.aice/bin/cua/`. An existing user `cua` entry is replaced only when its
   connection fields differ; enablement and tool filters are preferences.
4. saves the connection approval for that exact fingerprint, because the user
   just reviewed it. Tool calls still ask through Guard (once, per tool version
   for the Session, or for the service's current versions).

Setup requires idle state like other MCP configuration changes. A project MCP
service already named `cua` blocks setup. **Disable** disables `user:cua`;
the installation and OS grants stay. The service can also be edited, removed or
denied with `/mcp` like any other.

Settings shows a status row read from configuration and the MCP owner; it does
no native or MCP I/O. If tools report missing desktop access on Linux or
Windows, run `cua-driver doctor`.

## Runtime behavior

`cua-driver mcp` on macOS proxies to the signed CuaDriver.app daemon and starts
it when needed; on Linux and Windows the process owns its runtime. All use
Cua's default `standard` permission mode, which runs observation and input
without Cua prompts and denies foreign-process termination and unknown risky
operations. To narrow tools or resources, configure Cua's own capability
manifest or permission mode; see Cua's
[permission modes](https://cua.ai/docs/reference/cua-driver/permission-modes).

Each AICE process, and each future subagent, uses its own MCP connection and
therefore its own Cua session lifecycle. Cua does not isolate keyboard focus,
physical input or single-instance apps between sessions: background delivery
to different windows coexists, while concurrent foreground input on one desktop
can interfere. Cua's catalog is large, so it is usually reached through
`tool_search` rather than [preloaded](mcp.md#run-catalog-and-tool-search).
Screenshots reach image-capable models as ordinary MCP images; text-only models
receive a [text notice](contracts.md#structured-tool-outcomes) instead.

## Verification

Offline tests cover the definition and the Settings setup flow with synthetic
install and grant functions ([preset](../internal/desktop/preset_test.go),
[Settings TUI](../internal/app/settings_tui_test.go)). On macOS 2026-10-24,
`cua-driver mcp` 0.30.4 initialized over stdio with protocol `2025-06-18`,
started the CuaDriver.app daemon and listed 58 tools with 1.6 KB of server
instructions. Real-model desktop tasks, Linux and Windows remain unverified in
this design; Cua's own platform limits apply.
