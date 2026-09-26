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

### Computer Use helper (integration in progress)

Cua Driver is separate from automatic startup helpers. The macOS provisioning
API downloads only the pinned full 0.29.1 archive, verifies SHA-256, extracts the
signed App into a temporary directory under `/Applications`, and checks its
signature, Cua signing identity, Gatekeeper acceptance and version before an
exclusive rename to `/Applications/CuaDriver.app`. It preserves the license in
`~/.aice/bin/cua/0.29.1/LICENSE`. No bare Driver, Node addon or SDK runtime is
installed. See [provenance](../internal/deps/cua/VENDOR.md).
Verification subprocesses have a 20-second deadline and an 8 KiB stdout limit;
the limit also applies to the process pipe's buffered-copy path.

Provisioning respects the current instance's helper-download policy. It can
reuse a verified compatible existing App and refuses to overwrite an incompatible
or concurrently created installation. The installation directory lock waits up
to 30 seconds, is cancellable and is never stolen automatically. Directory
permission errors are reported; AICE does not invoke sudo, weaken signing checks,
change PATH or enable login autostart. Installation does not establish process
ownership or OS authorization.

macOS Settings setup invokes this API after the feature/install/authorization
disclosure. Its download policy is captured at AICE startup; a saved restart-only
change does not authorize a download in the current instance. Completed install
facts survive a later authorization or preference-save failure. Linux X11 runtime
integration and explicit Settings setup are implemented; broader native
acceptance and Windows runtime integration remain open. Linux setup installs
the private helper below, checks the display, and lets the user select a window
for a local capture test. It does not install system packages or grant desktop
permissions. The
[Computer Use status](desktop.md) records the exact scope of verification.

The Windows/Linux provisioning API installs into the private
`~/.aice/bin/cua/0.29.1/<os>-<arch>` directory (under the user profile on Windows).
It uses the same pinned release downloads, progress reporting and cancellable
installation lock. It selects only `cua-driver`, `cua-cursor-theme`, and, on
Windows, the signed `cua-driver-uia.exe` sibling, with the MIT license. It does
not install SDK libraries, Node addons or GNOME shell extensions. Each native
file has a reviewed hash; reuse verifies those hashes, rejects additional files
and links, and checks the license before running a version probe. Windows also
requires a valid timestamped Authenticode signature from `Cua AI, Inc.` on all
three executables, through system PowerShell without profiles. Verification
failure preserves the existing installation. Publication uses Linux
`RENAME_NOREPLACE` or Windows `MoveFile`, never a replace operation or fallback.
Rejected downloads are closed before cleanup so Windows can remove them.

The Linux version probe reports missing native loader/library dependencies;
AICE does not invoke a package manager. The pinned CLI depends on system
libX11, libXi, libxkbcommon and the GNU runtime, independently of the SDK files
excluded from installation. A successful version probe does not establish a
display, AT-SPI access, compositor support or desktop readiness. The private
Windows install does not grant UIAccess: the sibling's secure-path/signing
requirements remain OS policy, and AICE does not change registry policy, elevate
or move it to Program Files. No service, login task or shell extension starts
during provisioning. Native Linux arm64 installation has passed in a headless
Debian 13 container; Windows signature trust/installation and Linux amd64 native
installation still need their respective platform hosts. Cross-compilation and
desktop acceptance are recorded separately.

## Update

```sh
aice update            # install the latest release
aice update --check    # check without installing
aice update --force    # replace an unversioned/dev build
```

The interactive welcome screen checks for a newer release at most once every
hour. The TUI renders immediately, shows the check in progress, then
updates the welcome status line with the current, available, disabled, or unavailable
state. Development builds skip network access. Set `AICE_NO_UPDATE_CHECK=1` to
disable the check, or use `--no-update-check` / `"no_update_check": true`.
Both switches follow [configuration precedence](configuration.md#settings-and-precedence);
explicit `false` or `0` environment values override a lower-layer disable setting.

The command reports release discovery and download/checksum verification progress
on stderr, keeping the final result on stdout. In a terminal, a Bubbles progress
bar displays the archive download percentage based on bytes received and the
release asset size. Update and helper downloads share the ink theme’s sunset-to-gold
gradient, scaled across the filled portion, with Bubbles/Harmonica spring animation.
The printer owns the animation worker and joins it before subsequent logs or TUI
startup; completion and cancellation flush the actual received percentage without
waiting for the animation to settle. Downloads without a known size show received
MiB instead.
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
