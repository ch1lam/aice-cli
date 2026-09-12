---
name: browser
description: Use agent-browser for website interaction, screenshots, and web app testing. Use when building or changing local web UI, games, or visualizations that need browser verification, debugging UI behavior, or performing exploratory QA; also for navigation, forms, and data extraction.
---

# Browser use in AICE

This is AICE's integration guide. Load the official, version-matched command
instructions through the CLI as described below.

Run `agent-browser --version` first. If unavailable, tell the user to restart
AICE or check their network and `AICE_NO_DEP_INSTALL`. Browser automation is
not supported on Windows in this AICE version.

Do not run `agent-browser install`: it downloads a browser. If no system
Chrome, Chromium or Brave is found, relay the original `Chrome not found`
error and offer three user-controlled options: install Chrome/Chromium/Brave,
set `AGENT_BROWSER_EXECUTABLE_PATH`, or have the user run `agent-browser install`.
Do not silently download a browser.

AICE has already set `AGENT_BROWSER_SESSION` and browser directories. Do not
pass `--session`, export, unset, or otherwise change any `AGENT_BROWSER_*`
variable. Each bash invocation is a fresh shell. Browser state is not Session
history: after resuming a transcript, all old refs and tab IDs are invalid.

First read upstream instructions with `agent-browser skills get core`.
Use `agent-browser skills get core --full` when detailed command references
are needed. Follow the AICE session and authority rules here when upstream
examples use different flags or paths.

Use the loop: `open` → `snapshot -i` → action → `snapshot -i`.
Only use refs from the latest snapshot. Observe again after navigation,
form submission, tab switching, and dialogs. Never guess element refs.

## Verify web changes

After building or changing a web interface, use the browser to check the affected
behavior before handing it off, without waiting for a separate testing request.
Scale checks to the change and respect the user's requested scope.

Start or reuse the project's local server as needed. Check browser console and
page errors, exercise the relevant controls with real input, and confirm the
visible results. For canvas/WebGL, animation, or visual styling, inspect
screenshots with `read`; DOM state alone does not establish visual correctness.
Check the initial view and relevant states after interaction. After a fix, reload
and repeat the affected checks. Use `agent-browser skills get dogfood` for a
requested broader exploratory QA pass.

The native helper does not require Node.js. Check its actual availability before
skipping browser verification. If browser access or image inspection is blocked,
report the specific limitation and distinguish completed checks from unverified
visual or interactive behavior.

## Screenshots and session boundaries

For a named screenshot, explicitly pass the workspace-relative directory:

```sh
agent-browser screenshot .aice/browser/screenshots/page.png
```

Then use `read` on `.aice/browser/screenshots/page.png`. A bare `page.png`
does not use `AGENT_BROWSER_SCREENSHOT_DIR`; the directory applies only to
unnamed screenshots. With a non-vision model use `snapshot` or `get text`.
Avoid `--full` screenshots of long pages: read rejects images over 8000 pixels
on either side (and over 16 MiB).

When connected to the user's browser through `/browser`, first use
`agent-browser tab list --json` to identify the active bound tab. Cookies and
login state belong to the user and are shared across tabs. If `tab_gone` or a
connection failure occurs, stop, report it, and wait for user instructions.
Do not create or switch to pages the user has not authorized.

Cancellation does not undo browser actions. A command already received by the
daemon can continue, including a long wait that delays later commands.
After cancellation, obtain a fresh `snapshot -i` before continuing.

Do not use `close --all`, `--profile`, state save/load, auth commands,
`--cdp`, `--auto-connect`, or `connect`. External connections and tab selection
belong to the user through `/browser`.

Guard checks the bash command string and path access. It does not inspect or
restrict actions inside a web page. Ask the user before login, payment,
sending messages, deletion, or other consequential actions unless the user
has already explicitly authorized the action.
