# Browser automation

Browser automation is being integrated with the pinned native agent-browser
0.37.1 helper. The model uses the existing `bash` and image-capable `read` tools.
The Agent Loop, tool permissions and Session format remain unchanged.

## Helper installation

On macOS and Linux (amd64/arm64), startup installs the private helper into
`~/.aice/bin/agent-browser`. It does not use an arbitrary PATH installation.
The binary, version marker and complete upstream skill data must be present.
A mismatched version triggers installation on the next startup.

Installation messages and download progress go to stderr; redirected output
is a compact log. Downloads use pinned SHA-256 checksums, a bounded directory lock and staged
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

## Browser session ownership

`internal/browser.Manager` names sessions `aice-<pid>-<generation>` under
`~/.aice/browser/run`. It exposes the workspace screenshot directory and
versioned skill directory as environment values. Socket paths over 103 bytes
are rejected. Only a complete private pinned helper is executable.

External connections use `--pin-tab` and preserve `AGENT_BROWSER_CDP` or
`AGENT_BROWSER_AUTO_CONNECT` for subsequent commands. This environment extension
was approved after native testing showed that omitting the CDP target could
switch subsequent commands back to a local browser. CDP `targetId` selection
works when the connection is retained. Reconnecting an already used session
advances its generation to avoid stale bindings and daemon shutdown races.

Close is bounded to ten seconds and runs only when that session has a socket
or pid sidecar. It clears the manager's connection target. Startup sweep only
closes names with a demonstrably dead AICE owner; live or reused PIDs and
unrelated names are left alone. Browser state is not Session history.

## Interactive commands and lifetime

`/browser` offers Status, Connect to running browser (auto-detect), Connect to
port or URL, Choose tab, and Close. Connection prerequisites appear before
connecting. Port entry and tab selection use the existing cancellable command
prompt channel; they never enter model input or Session history. New tab is
the default; existing tabs are selected by CDP target identity. All browser
mutations reject an active model response.

Startup injects browser environment into the process so ordinary bash calls
inherit it. `/new` closes the old session, advances the generation and clears
the connection target. Cleanup errors are reported without preventing a new
conversation. Close also advances the generation to avoid reusing a daemon
that is shutting down. Interactive and print exits close the current browser
session with a bounded cleanup context even when the parent is cancelled.
The close acknowledgement is followed by waiting for socket/pid removal.

Connection target variables are unset/reset after close. A failed connection
is reported and retains its intended target until closed/replaced, so a later
command cannot silently operate the previous browser. `session info` cannot
prove a live external connection by itself; Status also queries tabs and
reports connection failure.
