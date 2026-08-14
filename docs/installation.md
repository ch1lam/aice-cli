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
the binary later.

## Runtime helpers

AICE uses Bash and ripgrep (`rg`). At startup it looks on `PATH` and in
`~/.aice/bin`:

- Missing ripgrep is downloaded for supported platforms.
- On Windows, missing Git Bash is downloaded as the Bash runtime.
- On macOS and Linux, Bash must already be available on the host.

Set `AICE_NO_DEP_INSTALL=1` to disable helper downloads. A missing helper only
disables the tools that require it; AICE reports the degraded capability.

## Update

```sh
aice update            # install the latest release
aice update --check    # check without installing
aice update --force    # replace an unversioned/dev build
```

The interactive welcome screen checks for a newer release at most once every
24 hours. The TUI renders immediately, shows the check in progress, then
updates the welcome card with the current, available, disabled, or unavailable
state. Development builds skip network access. Set `AICE_NO_UPDATE_CHECK=1` to
disable the check.

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
