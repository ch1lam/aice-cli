# Installation and Updates

## Supported releases

Prebuilt releases are published for:

| OS | Architectures | Archive |
| --- | --- | --- |
| macOS | `amd64`, `arm64` | `aice_darwin_<arch>.tar.gz` |
| Linux | `amd64`, `arm64` | `aice_linux_<arch>.tar.gz` |
| Windows | `amd64` | `aice_windows_amd64.zip` |

Installers and `aice update` verify release archives against `checksums.txt`.

## Install the latest release

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
```

The default destination is `~/.local/bin` (`%USERPROFILE%\.local\bin` on
Windows). Set `INSTALL_DIR` for another writable directory; relative paths resolve
against the current working directory. Windows updates user and current-shell
PATH; Unix prints a PATH command when needed.

Set `AICE_VERSION` to a release tag to pin a version; the scripts add a missing
`v` prefix. Otherwise they resolve the latest tag once for both the archive and
checksums. Downloads time out after 180 seconds per attempt and retry transient
failures up to twice.

The scripts verify and stage the executable before replacing it. Download,
checksum and staging failures preserve the installed binary. On Windows, close
running AICE processes if the executable is locked.

## Runtime helpers

AICE uses Bash and ripgrep (`rg`). At startup it looks on `PATH` and in
`~/.aice/bin`:

- Missing ripgrep is downloaded for supported platforms.
- On Windows, missing Git Bash is downloaded as the Bash runtime.
- On macOS and Linux, Bash must already be available on the host.

On Windows, AICE prefers its managed Git Bash, then Git Bash beside the `git.exe`
found on `PATH`, then standard Git for Windows installation directories. Other
native Bash executables in AICE's helper directory and absolute `PATH` entries
are fallbacks. If none is found, AICE downloads Git Bash unless helper downloads
are disabled. The mere presence of a WSL launcher does not prevent provisioning.

If native Bash is still unavailable (downloads were disabled or failed), the
`bash` tool tries WSL `bash.exe` launchers on `PATH`, including Windows system
directories and WindowsApps. A cancellable probe, with a five-second total
budget, checks that Bash runs and `wslpath` can map the workspace to an accessible
Linux directory. Broken WSL installations and missing distributions are rejected;
the tool reports the probe failure and Git for Windows installation guidance
(`winget install Git.Git`) if no usable shell remains. WSL commands use `-s` and
stdin transport rather than native Bash command-line arguments. WSL uses the
default distribution's tools and Linux paths; native file tools still use
Windows paths. See [execution and cancellation](execution-sessions.md#tool-execution-boundary).

On macOS and Linux, AICE also provisions a private, checksum-pinned native
`agent-browser` helper and its embedded upstream skills. Startup download
progress goes to stderr. AICE does not download a browser automatically; see
[Browser automation](browser.md) for supported platforms and connection setup.

Set `AICE_NO_DEP_INSTALL=1`, pass `--no-dep-install`, or set
`"no_dep_install": true` in settings to disable helper downloads. A missing helper only
disables the tools that require it; AICE reports the degraded capability.

### Computer Use helper

Cua is installed explicitly through `/desktop` or Settings, separately from
startup helpers. Setup respects the current process's helper-download policy;
saving a restart-only policy change does not alter this instance.

On macOS, setup verifies the pinned signed App before installing
`/Applications/CuaDriver.app`; incompatible existing installations are not
overwritten. Linux and Windows helper provisioning uses a private versioned
directory under `~/.aice/bin/cua/`. Native file hashes and licenses are checked;
Windows also requires the expected Authenticode signature. Pins and provenance
are maintained in [Cua VENDOR.md](../internal/deps/cua/VENDOR.md).

Setup does not invoke sudo or a package manager, change OS permissions, enable
autostart, or grant Windows UIAccess. Linux needs the Driver's system libraries
and a supported X11 display; setup includes choosing a window for a local capture
test. Installation or a version probe alone does not prove input support.
See [Computer Use](desktop.md) for setup, platform support and acceptance limits.

## Update

```sh
aice update            # install the latest release
aice update --check    # check without installing
aice update --force    # replace an unversioned/dev build
```

The welcome screen checks asynchronously and caches successful results for one
hour. Failed checks are not cached. Development builds skip the startup request.
Disable it with `AICE_NO_UPDATE_CHECK=1`, `--no-update-check`, or
`"no_update_check": true`; these follow normal
[configuration precedence](configuration.md#settings-and-precedence).

Progress goes to stderr and the final result to stdout. A download reaching 100%
still needs checksum verification and installation. Redirected output uses plain
stage messages. Release discovery times out after 15 seconds; download and
verification after three minutes. Ctrl+C cancels network work.

An unversioned build requires `--force` to update; `--check` still works.
An equal or newer installed version is retained unless forced. Detected Homebrew
paths are refused; write-permission failures report reinstall/package-manager
guidance. Other package managers are not detected automatically, so use their
own upgrade command for those installs.

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
