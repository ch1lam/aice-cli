# Roadmap

AICE aims to help developers maintain software over years, with code quality
as the main measure of progress: correctness, readability, useful tests and
the ease of future changes.

This roadmap describes priorities and direction, not release dates or available
features. The [README](README.md#current-capabilities) describes current
capabilities; [Releases](https://github.com/ch1lam/aice-cli/releases) records
shipped versions.

## Available today

- A Go runtime with an explicit Agent Loop, a TUI and a non-interactive CLI.
- Multiple model providers, file and shell tools, MCP and Agent Skills.
- Saved sessions, branches, context compaction and tool permission checks.
- Web, browser and desktop integrations with documented platform limits.
- [Harbor runs](docs/evaluation.md) through the ACP frontend, including Go and
  Python [maintenance tasks](docs/evaluation.md#maintenance-tasks) whose steps
  build on the agent's previous result.
  These are evaluation assets, not evidence of superior generated-code quality.

## Near-term priorities

| Priority | Work | Evidence of progress |
| --- | --- | --- |
| Authentication and connection management | Replace duplicated provider/MCP login flows and cumbersome account menus with shared operations and a simpler entry point, following the [SDK priority](docs/architecture.md#sdk-and-authentication-integration). | Supported official SDK flows take precedence; OAuth SDKs handle remaining protocol work; replaced paths and menus are removed. |
| Reliable everyday use | Close known gaps in sessions, cancellation, permissions and platform integrations. | Reproducible failures have regression coverage; platform claims have native verification. |
| Harness evaluation and ACP | Run AICE through the Agent Client Protocol so Harbor and editors use one standard interface; compare any two builds with paired, repeated runs and replay single decisions from recorded Sessions. See [Evaluation](docs/evaluation.md). | Any commit can be evaluated; results report noise, per-task flips and cost; every model input can be rebuilt from its Session. |
| Code quality evaluation | Evaluate feature work, bug fixes and behavior-preserving refactoring across successive changes with the Harbor [maintenance tasks](docs/evaluation.md#maintenance-tasks). | Comparable runs retain model/settings, tests, diffs and human review; improvements survive the next requirement. |
| Maintenance workflows | Improve support for incremental refactoring, large codebase changes, and documentation restructuring and migration. | Representative tasks preserve behavior, keep changes reviewable and leave code and docs aligned. |

Known implementation gaps live in [Maintenance](docs/maintenance.md), with
reproduction and verification details in their owning guides.

## Next

| Direction | Intended outcome | Starting point |
| --- | --- | --- |
| Shared runtime across interfaces | Keep model calls, tools, permissions and history independent of presentation. | The planned ACP frontend is the first additional interface built on the existing application and interaction boundaries. |
| MCP specification alignment | Register OAuth clients through Client ID Metadata Documents, and load server-provided Skills over MCP through the existing Skill trust path. | CIMD needs a hosted HTTPS client document and fixed loopback redirects; Skills over MCP waits for Go SDK support of `skills/list`/`skills/get`. See [MCP](docs/mcp.md). |

## Longer term

- **Cloud agent bot.** Run maintenance tasks through a hosted interface using the
  same runtime. Remote execution needs explicit workspace isolation, credentials,
  permissions, resource limits and recovery before it is ready for users.
- **Harness self-improvement.** Let AICE diagnose and propose improvements to its
  own harness. Changes must be tested and evaluated before adoption, with a
  review and rollback path.

Additional interfaces, cloud operation, integrated debugging infrastructure
and autonomous harness improvement are not shipped capabilities today.

## Help shape the roadmap

[Open an issue](https://github.com/ch1lam/aice-cli/issues/new/choose) with a
concrete maintenance problem, a representative workflow and how success could
be checked. English and Chinese are welcome. Discuss substantial changes before
implementing them; see [Contributing](CONTRIBUTING.md).

Keep this file about remaining direction. Link accepted work to its issue or PR;
when it ships, update the relevant user guide and remove the completed roadmap
item. Keep implementation history in Git and release notes.
