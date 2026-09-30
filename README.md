<h1 align="center">AICE</h1>

<p align="center">
  <strong>An open-source coding agent harness for long-term software maintenance</strong>
</p>

<p align="center">
  <a href="https://go.dev/"><img src="https://img.shields.io/github/go-mod/go-version/ch1lam/aice-cli" alt="Go version"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/ch1lam/aice-cli" alt="License"></a>
  <a href="https://github.com/ch1lam/aice-cli/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/ch1lam/aice-cli/ci.yml?branch=main&amp;label=build" alt="Build status"></a>
  <a href="https://github.com/ch1lam/aice-cli/releases/latest"><img src="https://img.shields.io/github/v/release/ch1lam/aice-cli" alt="Latest release"></a>
</p>

<p align="center">
  <img src="./assets/aice-header.png" alt="AICE coding agent" width="900">
</p>

<p align="center">
  English · <a href="./README-zh.md">简体中文</a><br>
  <a href="#quickstart">Quickstart</a> · <a href="./docs/README.md">Documentation</a> · <a href="./ROADMAP.md">Roadmap</a> · <a href="./CONTRIBUTING.md">Contributing</a>
</p>

## Install

AICE ships as a single Go binary for macOS, Linux and Windows. No Go toolchain
is needed to install a release.

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

AICE provisions helper tools as needed; browser and desktop automation have
additional platform requirements. See [Installation](./docs/installation.md)
for details, manual downloads and source builds. Update with `aice update`.

## Quickstart

Open a terminal in your project:

```sh
cd /path/to/project
aice
```

Run `/login` to configure a provider using an API key or a supported subscription
account, then `/model` to choose a model. Enter a task, for example:

```text
Explain how this project is organized and where I should start reading.
```

- Add `@src/main.go` to reference a file; use `Ctrl+V` or `Alt+V` to paste an image with a vision model.
- While AICE works, press Enter to send a correction, Ctrl+Enter to queue a follow-up, or Esc to cancel.
- Use `/history` to resume a session, `/settings` to change preferences, and `/help` or `?` for commands and shortcuts.

After configuring a provider, use `--print` for non-interactive tasks or scripts:

```sh
aice --print "Summarize the changes in this repository."
```

Add `--output-format json` for NDJSON events or `--session <path>` to save or
resume a session. Print mode has no interactive approval prompts; calls that
require approval are denied by default. See [CLI options](./docs/configuration.md#command-line-options).

## Who is it for?

Developers and maintainers working on long-lived codebases. AICE aims to improve
code quality, with a focus on readability, testability and the cost of future changes.

| Work | Example request |
| --- | --- |
| Everyday development | “Implement this feature using the project's conventions, with tests and docs.” |
| Incremental refactoring | “Simplify this module while preserving its behavior, and verify it with tests.” |
| Large-scale refactoring | “Map the dependencies, plan the migration, and work through it in verified steps.” |
| Documentation maintenance | “Check the docs against the code, remove outdated content, and reorganize the guides.” |

## Current capabilities

AICE manages model calls, tools, context and sessions through a TUI and
non-interactive CLI.

- **Choose your model.** Use OpenAI, Claude, DeepSeek, Kimi and other providers, or a custom OpenAI-compatible endpoint. See [supported providers](./docs/configuration.md#credentials-and-connection-overrides).
- **Work on your codebase.** Read, search and edit files; run shell commands, builds and tests.
- **Guide the agent.** Attach files and screenshots, send corrections while the agent works, queue follow-ups, or stop a run.
- **Resume your work.** Search saved sessions, pick up a previous conversation, or branch from an earlier message. Automatic context compaction supports longer tasks.
- **Extend your workflow.** Connect tools through [MCP](./docs/mcp.md) and reuse task instructions with [Agent Skills](./docs/configuration.md#agent-skills).

## Tools and integrations

| Capability | Setup and documentation |
| --- | --- |
| Web search and fetch | `/web` — configure Exa search and public-page fetching; [guide](./docs/web.md) |
| Browser automation | `/browser` — browse and interact with pages through agent-browser on macOS/Linux; [guide](./docs/browser.md) |
| Computer Use | `/desktop` — opt-in desktop automation through Cua Driver; [platform limits and verification status](./docs/desktop.md#platform-evidence) |
| MCP servers | `/mcp` or `aice mcp` — connect external tools and resources; [guide](./docs/mcp.md) |
| Agent Skills | `/skills` — discover reusable `SKILL.md` instructions; [guide](./docs/configuration.md#agent-skills) |

AICE runs tools with your local user permissions; the workspace is not an OS
sandbox. Model requests send task context to your configured provider.
See [tool permissions](./docs/execution-sessions.md#tool-execution-boundary)
and [Project Trust](./docs/project-trust.md) before working with sensitive projects.

## Roadmap

The next priorities are reliable everyday use, code quality evaluation and
maintenance workflows. Longer-term directions include a shared runtime across
interfaces, a cloud agent bot, built-in debugging and harness self-improvement.
See the [Roadmap](./ROADMAP.md) for current foundations, priorities and future work.

## Documentation and community

- [Documentation](./docs/README.md): setup, configuration, integrations and architecture.
- [Issues](https://github.com/ch1lam/aice-cli/issues/new/choose): ask questions, report bugs or propose improvements. English and Chinese are welcome.
- [Contributing](./CONTRIBUTING.md): development setup, verification and pull requests.
- [Security](./SECURITY.md) and [Code of Conduct](./CODE_OF_CONDUCT.md): reporting channels and community expectations.
- [Releases](https://github.com/ch1lam/aice-cli/releases): downloads and release notes.

AICE is maintained by [@ch1lam](https://github.com/ch1lam) and is under active
development. Session and configuration formats may change before a stable release.

## License

[Apache-2.0](./LICENSE).
