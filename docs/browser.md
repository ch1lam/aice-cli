# Browser automation

Browser automation is being integrated with the pinned native agent-browser
0.37.1 helper. The model uses the existing `bash` and image-capable `read` tools.
The Agent Loop, tool permissions and Session format remain unchanged.

## Helper installation

On macOS and Linux (amd64/arm64), startup installs the private helper into
`~/.aice/bin/agent-browser`. It does not use an arbitrary PATH installation.
The binary, version marker and complete upstream skill data must be present.
A mismatched version triggers installation on the next startup.

Downloads use pinned SHA-256 checksums, a bounded directory lock and staged
replacement. Failed replacement restores the previous files. Installation
failure is a warning, so coding remains available and startup retries later.
`AICE_NO_DEP_INSTALL` skips installation and reports browser automation
unavailable. Windows browser installation is disabled in this version.
No browser is downloaded: a system Chrome, Chromium or Brave is required.

The upstream skill tree and Apache-2.0 license are preserved in
[the vendored resources](../internal/deps/agentbrowser/VENDOR.md).

## Upstream verification

On macOS arm64, 2026-09-11, the native binary reports `agent-browser 0.37.1`
and runs with its existing ad-hoc signature; no re-signing is necessary.
GitHub release and npm binary bytes match for all four supported platforms.
A second independent network and native Linux verification remain pending.
The local Docker CLI has no running Docker daemon.

`skills get core --full` works with the vendored skill-data directory.
`tab list --json` returns `success`, `data.tabs`, and entries with `tabId`,
`targetId`, `title`, `url`, and `active`. `session info --json` returns
`data.active`, `data.pid`, `data.runtime.browserLaunched` and page count;
it does not expose connection mode or the bound tab directly.

`AGENT_BROWSER_SCREENSHOT_DIR` applies only when screenshot has no explicit
path. For a named screenshot use the complete workspace-relative path
`.aice/browser/screenshots/page.png`, then read that same path.
Relative paths are resolved by the daemon, not each later CLI invocation.

Cancellation kills the CLI, not an already issued browser action. A cancelled
long wait can still occupy the daemon, delaying subsequent observation until
it ends. Re-observe before continuing. Native Linux and interactive
`chrome://inspect/#remote-debugging` acceptance remain unverified.
