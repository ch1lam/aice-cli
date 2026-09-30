# Contributing to AICE

Bug reports, documentation improvements, regression tests and code changes are
welcome. Issues and pull requests may be written in English or Chinese. Follow
the [Code of Conduct](CODE_OF_CONDUCT.md) when participating.

## Find the right place

- Questions, bugs and proposals: search [existing issues](https://github.com/ch1lam/aice-cli/issues),
  then [open an issue](https://github.com/ch1lam/aice-cli/issues/new/choose).
- Security vulnerabilities: follow [Security](SECURITY.md); keep exploit details private.
- Product direction: read the [Roadmap](ROADMAP.md).
- Substantial features, new dependencies, or changes to runtime and permission
  boundaries: discuss the problem and approach in an issue before implementation.
  Small fixes and documentation corrections can go straight to a PR.

Include a minimal reproduction for bugs, with AICE version, OS, relevant
provider/model and expected versus actual behavior. Remove credentials and
private project data from logs, screenshots and session excerpts.

## Set up a checkout

Fork the repository on GitHub, clone your fork, and create a branch from `main`.
Use the Go version in [go.mod](go.mod), Git, Bash and ripgrep (`rg`). Race tests
also need a working C compiler. Platform and helper details are in
[Installation](docs/installation.md).

From the repository root:

```sh
go build -o ./aice ./cmd/aice
./aice --help
```

On Windows, build with `-o ./aice.exe` and run `./aice.exe --help`.
Provider credentials are needed for real model requests, not for the default
test suite.

## Make and verify a change

Read [AGENTS.md](AGENTS.md) and [Architecture](docs/architecture.md) for the task
map and package boundaries. Follow [Go quality](docs/go-quality.md) for code
changes and [Collaboration](docs/collaboration.md#verification-commands) for
the required checks. No particular AI tool or local skill library is required.

Keep each PR focused on one problem. Preserve behavior during refactoring,
add regression coverage for bugs, and update the document that owns the changed
behavior. Keep `README.md` and `README-zh.md` in sync.

For Go changes, format modified files and run:

```sh
go test ./...
go vet ./...
```

The collaboration guide adds race, lint and integration checks where needed.
Documentation-only changes need `git diff --check` and checks of changed links,
anchors and examples; they do not require the full Go suite. External services,
paid model calls and native desktop tests are separate, opt-in checks.

## Submit a pull request

Target `main`. Explain the problem, resulting behavior and verification results,
including checks you could not run. For visible UI changes, include a screenshot
or recording when useful. Draft PRs are welcome for early feedback.

AI-assisted contributions follow the same standard: review the entire diff,
understand the change, and verify the claims in the PR. You are responsible for
the submitted code and documentation.

Contributions are made under the project's [Apache-2.0 license](LICENSE).
