# Security policy

## Report a vulnerability

Use GitHub's [private vulnerability reporting](https://github.com/ch1lam/aice-cli/security/advisories/new)
to contact the maintainers. Do not disclose unpatched vulnerabilities in public
issues or pull requests.

Include:

- The AICE version or commit, OS and affected component.
- A minimal reproduction, expected boundary and observed behavior.
- The impact, prerequisites and any suggested fix or mitigation.

Use synthetic data. Do not include real API keys, credentials or private project
contents. English and Chinese reports are welcome. Use the private advisory
thread for follow-up and coordinate public disclosure with the maintainers.

## Supported versions

Security fixes target the latest release and current development on `main`.
Older releases do not have a separate backport commitment. Report the version
you observed; do not run a dangerous reproduction again just to test an update.
Downloads and release notes are on [Releases](https://github.com/ch1lam/aice-cli/releases).

This project does not promise a fixed response or remediation time.

## Execution boundaries

AICE runs tools with the privileges of its process. Tool permission checks and
Project Trust do not provide OS-level isolation. Model requests send task
context to the configured provider; connected tools can access external
services. Use a container or VM when host isolation is required.

For the actual boundaries and limits, see [tool execution](docs/execution-sessions.md#tool-execution-boundary),
[Project Trust](docs/project-trust.md), and the [integration guides](docs/README.md#using-aice).
Report violations of documented permission, credential or history boundaries
through the private channel above. Ordinary bugs and usage questions belong in
[Issues](https://github.com/ch1lam/aice-cli/issues/new/choose).
