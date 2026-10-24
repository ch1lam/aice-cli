# Browser automation

Browser automation is the pinned agent-browser helper running as an ordinary
MCP service, `user:browser`. AICE provisions the helper and writes the service
definition; everything after that is the generic [MCP](mcp.md) path:
connection approval, tool search, Guard tool approval, results with images,
Session retention and readback. There is no browser manager, bash environment,
browser skill or `browser_headed` setting.

agent-browser owns its sessions, daemon, Chrome processes, tabs and tool
semantics. Its MCP tools (the default `core` profile: navigation, snapshots,
interaction, waits, reads, screenshots, JavaScript evaluation, tab basics and
close) and server instructions reach the model like any other service's.

## Installation

On macOS and Linux (amd64/arm64), startup provisions the private pinned
`~/.aice/bin/agent-browser` 0.37.1 and `agent-browser.version`; see
[runtime helpers](installation.md#runtime-helpers) and
[provenance](../internal/deps/agentbrowser/VENDOR.md). `AICE_NO_DEP_INSTALL=1`
disables the download. Windows browser automation is unavailable in this
version. AICE never downloads Chrome: install Chrome, Chromium or Brave, or run
`agent-browser install` yourself. Like every stdio MCP service, the helper
inherits only a small base environment, so options such as
`AGENT_BROWSER_EXECUTABLE_PATH` belong in the service `env`, for example
`{"env": "AGENT_BROWSER_EXECUTABLE_PATH"}` to forward your shell value.

## Set up and change

Use `/browser`, or the Browser rows in Settings → Tools & Network. Each choice
rewrites the `user:browser` definition and saves its connection approval,
because the user just chose it; tool calls still ask through Guard.

Settings shows **Browser automation** with the service state and, once set up,
its status above Set up / Repair, Close browser session and Disable. **Show
window** toggles on Enter and **Connect to** chooses the target. `/browser`
offers every choice in one menu.

| Action | Effect on the service |
| --- | --- |
| Set up / Repair | Add, replace or re-enable `user:browser` with the current choices |
| Show window (toggle) | `AGENT_BROWSER_HEADED=true/false`; applies when agent-browser next launches a browser |
| Connect to running browser (auto-detect) | `AGENT_BROWSER_AUTO_CONNECT=true` |
| Connect to port or URL… | `AGENT_BROWSER_CDP=<port or ws:// / wss:// URL>` |
| Use a separate browser | Clears the connection target; agent-browser launches a browser with a temporary profile |
| Close browser session | Runs `agent-browser --session aice close`: a launched browser exits, a connected one is only disconnected |
| Disable | Disables `user:browser`; the helper stays installed |

The definition is:

```json
{"name": "Browser (agent-browser)", "transport": "stdio",
 "command": "~/.aice/bin/agent-browser (absolute)", "args": ["mcp"],
 "env": {"AGENT_BROWSER_SESSION": {"value": "aice"},
         "AGENT_BROWSER_HEADED": {"value": "false"},
         "AGENT_BROWSER_IDLE_TIMEOUT_MS": {"value": "600000"}}}
```

A rewrite keeps the rest of the service: tool filters, timeouts and env entries
other than the preset's five keys stay as you set them. Changes require idle
state like other MCP configuration changes. A project
service already named `browser` blocks setup. The service can also be edited
with `/mcp` like any other; Settings shows its state from configuration
without helper or MCP I/O.

## Connecting to your browser

For an explicit connection, start a dedicated profile yourself:

```sh
# macOS; Linux can use chromium or google-chrome with the same arguments.
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
```

Then choose Connect to port or URL and enter `9222` or a `ws://`/`wss://`
endpoint. Chrome 136+ requires a non-default profile for remote debugging; see
[Chrome's explanation](https://developer.chrome.com/blog/remote-debugging-port).
Auto-detect uses `chrome://inspect/#remote-debugging` on Chrome versions that
support it and may ask you to Allow the connection. A connected browser shares
its login state and tabs with the model's tools; the debugging port is reachable
by other local processes.

## Lifetime and concurrency

The MCP process is an ordinary owned stdio child and is closed with AICE. The
`aice` session's daemon and the Chrome it launched outlive that process until
agent-browser's idle timeout (ten minutes as configured) or Close browser
session. `/new` and Session switching do not reset browser state, and history
never restores it; observe the page again when resuming. All AICE processes,
and future subagents, share the `aice` session unless a tool call passes another
`session`; parallel use of one session can interfere.

## Screenshots and authority

Screenshots return as MCP images: image-capable models see them, text-only
models receive a [text notice](contracts.md#structured-tool-outcomes), and the
Session retains the source. agent-browser also writes screenshot files under
its own directory. Browser tools act on real pages and can submit forms; Guard
approval of a tool is not page-level authorization, and AICE adds no domain
policy. agent-browser's own controls (`AGENT_BROWSER_ALLOWED_DOMAINS`,
`--action-policy`, `--confirm-actions`) can be added to the service `env`.

## Verification

Offline tests cover the definition and every `/browser` choice through Settings
([preset](../internal/browser/preset_test.go),
[actions](../internal/app/management_actions_test.go)). On macOS 2026-10-25,
the pinned 0.37.1 `agent-browser mcp` connected as `user:browser` with 29 core
tools; through AICE's MCP client it opened a page (returning structured
content), returned a PNG screenshot block and closed. Real-model browsing,
external CDP connection and Linux remain unverified in this design.
