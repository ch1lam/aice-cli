#!/bin/sh
# Install AICE into a user-writable directory so `aice update` can later
# replace the binary in place. Linux and macOS only; Windows users should use
# a package manager or a manual download.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
#   INSTALL_DIR=~/bin sh -c "$(curl -fsSL ...)"   # override install directory

set -eu

repo="ch1lam/aice-cli"
binary="aice"
install_dir="${INSTALL_DIR:-$HOME/.local/bin}"
base="https://github.com/${repo}/releases/latest/download"

log() { printf 'aice: %s\n' "$*" >&2; }
fail() { printf 'aice: error: %s\n' "$*" >&2; exit 1; }

[ -n "${INSTALL_DIR:-}" ] && log "using INSTALL_DIR=${INSTALL_DIR}"

case "$(uname -s)" in
	Darwin) goos="darwin" ;;
	Linux) goos="linux" ;;
	*) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
	x86_64|amd64) goarch="amd64" ;;
	arm64|aarch64) goarch="arm64" ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
esac

bundle="aice_${goos}_${goarch}.tar.gz"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/aice-install.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

log "downloading ${bundle} ..."
curl -fsSL -o "${tmp}/${bundle}" "${base}/${bundle}"
curl -fsSL -o "${tmp}/checksums.txt" "${base}/checksums.txt"

want="$(awk -v name="${bundle}" '$2 == name { print $1 }' "${tmp}/checksums.txt")"
[ -n "$want" ] || fail "checksums.txt has no entry for ${bundle}"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "${tmp}/${bundle}" | awk '{ print $1 }')"
else
	got="$(shasum -a 256 "${tmp}/${bundle}" | awk '{ print $1 }')"
fi
[ "$want" = "$got" ] || fail "checksum mismatch for ${bundle}"

log "installing to ${install_dir} ..."
mkdir -p "$install_dir"
tar -xzf "${tmp}/${bundle}" -C "${tmp}" "${binary}"
install -m 0755 "${tmp}/${binary}" "${install_dir}/${binary}"

case ":$PATH:" in
	*":${install_dir}:"*) ;;
	*)
		log "${install_dir} is not on PATH; add it to your shell profile:"
		printf '  export PATH="%s:$PATH"\n' "$install_dir" >&2
		;;
esac

log "installed ${install_dir}/${binary}"
log "run \`aice --version\` to verify, and \`aice update\` to upgrade later"
