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
- Initial [Go](evals/go-service/README.md) and [Python](evals/python-cli/README.md)
  maintenance evaluation tasks, plus a [Harbor adapter](integrations/harbor/README.md).
  These are evaluation assets, not evidence of superior generated-code quality.

## Near-term priorities

| Priority | Work | Evidence of progress |
| --- | --- | --- |
| Reliable everyday use | Close known gaps in sessions, cancellation, permissions and platform integrations. | Reproducible failures have regression coverage; platform claims have native verification. |
| Code quality evaluation | Evaluate feature work, bug fixes and behavior-preserving refactoring across successive changes. | Comparable runs retain model/settings, tests, diffs and human review; improvements survive the next requirement. |
| Maintenance workflows | Improve support for incremental refactoring, large codebase changes, and documentation restructuring and migration. | Representative tasks preserve behavior, keep changes reviewable and leave code and docs aligned. |

Known implementation gaps live in [Maintenance](docs/maintenance.md), with
reproduction and verification details in their owning guides.

## Next

| Direction | Intended outcome | Starting point |
| --- | --- | --- |
| Harness debugging and evaluation | Inspect why a run failed, reproduce it, and compare a proposed harness change. | Build on Session history and existing evaluation tasks. |
| Shared runtime across interfaces | Keep model calls, tools, permissions and history independent of presentation. | Build on the existing application and interaction boundaries; validate another interface against the same runtime contracts. |

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
