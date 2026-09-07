#!/bin/sh
# Install AICE into a user-writable directory so `aice update` can later
# replace the binary in place. Linux and macOS only; Windows users should use
# scripts/install.ps1 (PowerShell).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh | sh
#   INSTALL_DIR=~/bin sh -c "$(curl -fsSL ...)"   # override install directory
#   AICE_VERSION=v1.2.3 sh -c "$(curl -fsSL ...)" # pin a release tag

set -eu

repo="ch1lam/aice-cli"
binary="aice"
install_dir="${INSTALL_DIR:-$HOME/.local/bin}"

log() { printf 'aice: %s\n' "$*" >&2; }
fail() { printf 'aice: error: %s\n' "$*" >&2; exit 1; }

[ -n "${INSTALL_DIR:-}" ] && log "using INSTALL_DIR=${INSTALL_DIR}"

# Bound every request, including redirects, and retry transient failures twice.
fetch() {
	curl -fsSL --connect-timeout 15 --max-time 180 --retry 2 --retry-max-time 600 "$@"
}

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
staging=""
cleanup() {
	rm -rf "$tmp"
	[ -z "$staging" ] || rm -rf "$staging"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM

version="${AICE_VERSION:-}"
if [ -n "$version" ]; then
	case "$version" in v*) ;; *) version="v${version}" ;; esac
	base="https://github.com/${repo}/releases/download/${version}"
else
	# Resolve latest once so a release published between downloads cannot mix assets.
	latest="$(fetch -o /dev/null -w '%{url_effective}' "https://github.com/${repo}/releases/latest")" ||
		fail "could not resolve latest release; check your network and retry"
	case "$latest" in
		"https://github.com/${repo}/releases/tag/"?*)
			version="${latest##*/}"
			base="https://github.com/${repo}/releases/download/${version}"
			;;
		*) fail "unexpected latest release URL: ${latest}" ;;
	esac
fi
log "installing ${version}"

log "downloading ${bundle} ..."
fetch -o "${tmp}/${bundle}" "${base}/${bundle}" ||
	fail "could not download ${bundle} for ${version}; check the release tag and network"
fetch -o "${tmp}/checksums.txt" "${base}/checksums.txt" ||
	fail "could not download checksums.txt for ${version}; check the release assets and network"

want="$(awk -v name="${bundle}" '$2 == name { print $1 }' "${tmp}/checksums.txt")"
[ -n "$want" ] || fail "checksums.txt has no entry for ${bundle}"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "${tmp}/${bundle}" | awk '{ print $1 }')"
else
	got="$(shasum -a 256 "${tmp}/${bundle}" | awk '{ print $1 }')"
fi
[ "$want" = "$got" ] || fail "checksum mismatch for ${bundle}"

tar -xzf "${tmp}/${bundle}" -C "${tmp}" "${binary}"
[ -f "${tmp}/${binary}" ] && [ ! -L "${tmp}/${binary}" ] || fail "archive must contain a regular ${binary} file"
mkdir -p -- "$install_dir"
install_dir="$(CDPATH= cd -- "$install_dir" && pwd -P)"
log "installing to ${install_dir} ..."
[ ! -d "${install_dir}/${binary}" ] || fail "${install_dir}/${binary} is a directory"
# Stage on the destination filesystem; rename only after copying has succeeded.
staging="$(mktemp -d "${install_dir}/.aice-install.XXXXXX")"
install -m 0755 "${tmp}/${binary}" "${staging}/${binary}" || fail "could not stage ${binary}; existing installation was not replaced"
mv -f "${staging}/${binary}" "${install_dir}/${binary}" || fail "could not replace ${binary}; existing installation was not replaced"

case ":$PATH:" in
	*":${install_dir}:"*) ;;
	*)
		log "${install_dir} is not on PATH; add it to your shell profile:"
		# Single-quote the directory so spaces and shell metacharacters stay literal.
		quoted_dir="$(printf '%s' "$install_dir" | sed "s/'/'\\\\''/g")"
		printf "  export PATH='%s':\"\$PATH\"\n" "$quoted_dir" >&2
		;;
esac

log "installed ${install_dir}/${binary}"
log "run \`aice --version\` to verify, and \`aice update\` to upgrade later"
