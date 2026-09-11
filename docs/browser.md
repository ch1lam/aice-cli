# Browser automation

AICE uses the pinned native `agent-browser` 0.37.1 helper through the existing
`bash` and image-capable `read` tools. Load the builtin `browser` skill for the
AICE workflow, then `agent-browser skills get core` for upstream instructions.
There is no additional tool type, service, Node runtime, or Session format.

## Installation and first use

On macOS and Linux (amd64/arm64), startup provisions
`~/.aice/bin/agent-browser`, `agent-browser.version`, and
`agent-browser-skills/0.37.1/`. AICE requires this private pinned installation,
not an arbitrary helper on PATH. A version mismatch triggers replacement.
All nine upstream skills and their references are embedded in AICE and extracted
with the helper. They are upstream command documentation, not extra entries in
the AICE skill catalog. See [source and license provenance](../internal/deps/agentbrowser/VENDOR.md).

Downloads use pinned SHA-256 checksums, a bounded installation lock and staged
replacement. A failed replacement attempts to restore previous files; backups
are retained if rollback itself fails. Dead Unix lock owners can be reclaimed;
unknown ownership is left alone and waiting is bounded. Installation warnings
do not prevent coding, and the next startup retries. Progress goes to stderr:
a terminal bar for known sizes, received bytes for unknown sizes, compact logs
when redirected. Completion of the download precedes verification/installation.

`AICE_NO_DEP_INSTALL=1` disables downloads. An already complete pinned helper
remains usable offline; otherwise startup reports browser automation unavailable.
Windows browser automation is disabled in this version; ordinary coding remains
available. AICE never downloads Chrome automatically. Install Chrome, Chromium
or Brave yourself, set `AGENT_BROWSER_EXECUTABLE_PATH` to an installed executable,
or explicitly run `agent-browser install` if you want upstream's browser download.

For a separate browser with a temporary profile, simply ask AICE to browse a page.
The first `open` starts the browser lazily. It has no access to your existing
browser login state. Observe with `snapshot -i` before acting and after each page
change. Element refs from older observations must not be reused.

## Connect to a running browser

Use `/browser` to open Status, Connect to running browser (auto-detect),
Connect to port or URL, Choose tab, or Close. `/browser status` is also accepted.
Connection prerequisites are shown before connecting. Port/URL entry and tab
selection are transient application prompts, never model messages or Session
history. Mutation commands are unavailable while the model is running.

For an explicit connection, start a dedicated browser profile yourself:

```sh
# macOS; Linux can use chromium or google-chrome with the same arguments.
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
```

Then select Connect to port or URL and enter `9222` or a browser `ws://`/`wss://`
endpoint. Chrome 136+ requires a non-default profile for remote debugging flags;
see [Chrome's explanation](https://developer.chrome.com/blog/remote-debugging-port).
Sign in yourself in that profile if needed. Credentials and cookies remain
owned by your browser; AICE does not export profiles or save authentication state.

For auto-detect, enable remote debugging in `chrome://inspect/#remote-debugging`
on a Chrome version that supports it and approve Chrome's connection prompt.
See [Chrome's session-debugging guide](https://developer.chrome.com/blog/chrome-devtools-mcp-debug-your-browser-session).
Availability depends on the installed Chrome and upstream discovery support.
Status reports connection errors; it does not treat a running daemon as proof
that the external browser is connected.

Connections pin a new tab by default. You can instead select an existing tab by
its CDP target identity in Choose tab. Existing tabs share the browser's login
state. A closed bound tab or lost connection requires reporting the error and
asking the user how to continue; the skill prohibits silently replacing the tab.
Disconnecting keeps the user's browser and its tabs, including AICE-created tabs.

## Ownership and lifetime

`internal/browser.Manager` owns one `aice-<pid>-<generation>` browser session per
AICE process. `internal/app` wires its environment and serializes lifecycle
changes at idle command boundaries. The Agent Loop and Guard are unchanged.
Browser state is ephemeral and is never restored from Session JSONL. Resuming a
conversation requires fresh page observation, even if history contains old refs.

| Environment variable managed by AICE | Value |
| --- | --- |
| `AGENT_BROWSER_SESSION` | Current process/generation name |
| `AGENT_BROWSER_SOCKET_DIR` | `~/.aice/browser/run` |
| `AGENT_BROWSER_SCREENSHOT_DIR` | Absolute workspace `.aice/browser/screenshots` |
| `AGENT_BROWSER_SKILLS_DIR` | Private versioned upstream skill directory |
| `AGENT_BROWSER_CDP` | Selected endpoint; unset when no explicit target exists |
| `AGENT_BROWSER_AUTO_CONNECT` | Current auto-detect setting |

The last two variables retain the selected external connection across fresh bash
invocations. Without them, upstream 0.37.1 can switch a later command back to a
local browser. The model must not override these variables, use `--session`,
connect directly, save/load profiles, or use `close --all`. The user-controlled
`AGENT_BROWSER_EXECUTABLE_PATH` remains available for selecting an executable.

`/new` and Close disconnect the current session, advance the generation and clear
the external target. A failed connection retains the intended target until it is
closed or replaced, so subsequent commands cannot fall back to the old target.
Reconnecting a used session also advances the generation. `/new` reports cleanup
errors but still starts a fresh conversation. Interactive and print exits use a
bounded cleanup context even after cancellation. Close waits up to ten seconds
for the socket/pid sidecars to disappear, beyond the upstream acknowledgement.

Startup only sweeps matching session names whose AICE owner is demonstrably dead.
Live/reused PIDs and unrelated names are retained. A hard kill can leave a daemon
until this sweep. Socket paths longer than 103 bytes are rejected with a warning.
Runtime and screenshot directories are created with mode 0700; screenshots are
not deleted by close or `/new`.

Cancelling a tool kills its CLI process, not browser actions already received by
the daemon. It does not undo navigation or submission. A cancelled long wait can
occupy the daemon and delay the next command until it finishes. Re-observe before
continuing; this upstream behavior is not a promise of immediate cancellation.

## Screenshots and authority

For a named screenshot use an explicit workspace-relative path:

```sh
agent-browser screenshot .aice/browser/screenshots/page.png
```

Then `read` that same path. `AGENT_BROWSER_SCREENSHOT_DIR` only applies to unnamed
screenshots. Explicit relative paths resolve against the daemon's working
directory, not a later CLI invocation's directory. Avoid oversized full-page
images: `read` rejects images over 8000 pixels on either side or over 16 MiB.
A non-vision model should use snapshots/text instead.

Guard checks bash command strings and path access. It does not inspect website
actions, isolate domains, or distinguish reading from payment, message sending,
or deletion within a browser command. The browser skill requires authorization
for consequential actions, but a skill is guidance, not an enforcement boundary.
`--yolo` and print mode retain their [existing Guard semantics](execution-sessions.md#tool-execution-boundary).
The workspace boundary is not browser isolation.

## Maintenance and verification

To upgrade, verify the upstream native release and supported platform assets;
update the version, asset names and SHA-256 values in
[versions.go](../internal/deps/versions.go). Replace the entire vendored skill tree,
license and provenance together. Compare release bytes with the same-version npm
package, review upstream CLI/JSON/environment changes, then run the offline suite
and [native integration check](collaboration.md#browser-checks). Do not run an
unverified downloaded helper as part of checksum collection.

The v0.37.1 release and npm bytes matched on all four supported platforms.
Verification date: 2026-09-11. macOS arm64 native execution accepts the existing
ad-hoc signature. Native tests ran on macOS arm64 with Chrome 153 and Linux arm64 with Chromium 152 in a disposable
container. A second independent download network was not available. The following
acceptance record distinguishes tests from full interactive/model acceptance.

| Case | macOS arm64 | Linux arm64 |
| --- | --- | --- |
| V1 installation/progress; V3 upgrade | Offline HTTP fixtures, checksum and replacement tests pass | Release bytes verified; native helper runs; installer fixture suite not run natively |
| V2 disabled/offline | Fixture tests and isolated offline TUI pass | Native helper runs offline; full startup not exercised |
| V4 page/form/screenshot | Native open/snapshot/fill/click/title/PNG check passes; real model vision not run | Same native test passes; real model vision not run |
| V5 missing browser | Upstream error behavior inspected; model guidance tested as skill contract | Not exercised |
| V6 cancel | Native probe: CLI cancellation leaves queued wait; later snapshot recovers | Not exercised |
| V7 new session; V8 exit | Native close/rotate test and actual `/new`, `/quit` TUI exercised | Native close/rotate test passes; TUI not exercised |
| V9 stale owner sweep | Dead/live/foreign owner unit tests; no full kill/restart acceptance | Not exercised natively |
| V10 print cleanup | Application cleanup tests; real model print not run | Not exercised |
| V11 CDP prerequisites | Dedicated profile + actual TUI connection and new-tab choice pass | Not exercised |
| V12/V13 new/existing tab | Native CDP probes and target selection; no model-driven complete flow | Not exercised |
| V14 tab gone; V15 browser gone | Error handling tests/source inspection; full user interaction not exercised | Not exercised |
| V16 multi-instance | Name isolation and owner-sweep unit tests; simultaneous native CDP flows not exercised | Not exercised |
| V17 external disconnect | Native probes and TUI `/new` preserve dedicated Chrome and both tabs | Not exercised |
| V18 inspect auto-detect | Actual Chrome Allow/login-state acceptance not exercised | Not exercised |
| V19 Guard | Existing screenshot-path/open-command semantics covered by unit tests | Same portable tests; not run natively |

Unverified acceptance items are tracked in [Maintenance](maintenance.md#browser-acceptance-gaps).
