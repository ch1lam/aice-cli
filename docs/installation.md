# Installation and Updates

## Supported releases

Prebuilt releases are published for:

| OS | Architectures | Archive |
| --- | --- | --- |
| macOS | `amd64`, `arm64` | `aice_darwin_<arch>.tar.gz` |
| Linux | `amd64`, `arm64` | `aice_linux_<arch>.tar.gz` |
| Windows | `amd64` | `aice_windows_amd64.zip` |

The install scripts and self-update command check release archives against
`checksums.txt` before replacing a binary.

## Install the latest release

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

The scripts install into `~/.local/bin` (or
`%USERPROFILE%\.local\bin`) by default. Set `INSTALL_DIR` to use another
user-writable directory. A user-writable install lets `aice update` replace
the binary later. Relative `INSTALL_DIR` values resolve against the current
working directory; PATH instructions and Windows PATH entries use the absolute
installation directory. On Windows the script updates both the per-user PATH
and the current shell's PATH, comparing complete entries without regard to case.
On macOS and Linux it prints a shell-quoted PATH command when needed.

Set `AICE_VERSION` to a GitHub release tag (for example `v1.2.3`) to pin the
download for evaluation or CI. If the value has no `v` prefix, the scripts
add one. Unset, they resolve the latest release once and download both the
archive and checksums from that same tag. Downloads have a 180-second timeout
per attempt and up to two retries for transient failures; Unix also sets a
15-second connection timeout.

The installers verify and extract the archive before staging the executable in
the destination directory. They then rename (Unix) or replace (Windows) the
staged file, rather than copying over the live executable. Download, checksum,
and staging failures leave the existing executable intact. Windows refuses a
replacement when the executable is locked; close running AICE processes and
retry. Temporary download and staging files are cleaned up on normal completion
and handled failures.

## Runtime helpers

AICE uses Bash and ripgrep (`rg`). At startup it looks on `PATH` and in
`~/.aice/bin`:

- Missing ripgrep is downloaded for supported platforms.
- On Windows, missing Git Bash is downloaded as the Bash runtime.
- On macOS and Linux, Bash must already be available on the host.

On macOS and Linux, AICE also provisions a private, checksum-pinned native
`agent-browser` helper and its embedded upstream skills. Startup download
progress goes to stderr. AICE does not download a browser automatically; see
[Browser automation](browser.md) for supported platforms and connection setup.

Set `AICE_NO_DEP_INSTALL=1` to disable helper downloads. A missing helper only
disables the tools that require it; AICE reports the degraded capability.

## Update

```sh
aice update            # install the latest release
aice update --check    # check without installing
aice update --force    # replace an unversioned/dev build
```

The interactive welcome screen checks for a newer release at most once every
hour. The TUI renders immediately, shows the check in progress, then
updates the welcome card with the current, available, disabled, or unavailable
state. Development builds skip network access. Set `AICE_NO_UPDATE_CHECK=1` to
disable the check.

The command reports release discovery and download/checksum verification progress
on stderr, keeping the final result on stdout. In a terminal, a Bubbles progress
bar displays the archive download percentage based on bytes received and the
release asset size. Downloads without a known size show received MiB instead.
The bar reaching 100% means the archive has downloaded; checksum verification
and installation follow with a separate status. Redirected output uses compact
plain-text stage messages. Failed or canceled downloads end the progress line
before the error is printed. Release discovery has a 15-second
timeout; download and checksum verification have a three-minute timeout. Ctrl+C
cancels network work. A timeout reports a retry hint. Unversioned builds fail
before network access unless `--force` is supplied; `--check` can still report
the latest release and the command needed to replace a development build.
When the installed version is newer than the latest release, it is retained and
the result displays the installed version. `--force` bypasses this comparison.

`aice update` refuses package-manager-owned installs and non-writable
executables. Use the package manager in those cases.

## Manual download

Download the matching archive from [GitHub
Releases](https://github.com/ch1lam/aice-cli/releases), verify it against the
release's `checksums.txt`, then place `aice` (or `aice.exe`) in a writable
directory on `PATH`.

## Build from source

Use the Go version declared in [`go.mod`](../go.mod):

```sh
git clone https://github.com/ch1lam/aice-cli.git
cd aice-cli
go build -o ./aice ./cmd/aice
./aice --version
```

Source builds without a version stamp report `dev` and send `aice/dev` in model
requests. Release builds stamp `internal/buildinfo.Version`; the same value is
used by the CLI, TUI, update checks, and User-Agent. For a versioned source build,
set the actual source version through the release workflow's linker target:

```sh
go build -ldflags "-X github.com/ch1lam/aice-cli/internal/buildinfo.Version=<actual-version>" -o ./aice ./cmd/aice
```

Replace `<actual-version>` with the version of the source being built, not a
provider client's name or version. The [release workflow](../.github/workflows/release.yml)
is the authority for packaged release builds.
