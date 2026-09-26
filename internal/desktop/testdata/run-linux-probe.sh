#!/bin/sh
# Run only in a disposable Debian container. Do not mount the host display,
# D-Bus, home or /dev/input. Arguments: compiled Linux test binary, pinned archive.
set -eu
[ -f /.dockerenv ] || { echo 'Requires an isolated Docker container' >&2; exit 1; }
[ "$(id -u)" = 0 ] || { echo 'Container package preparation requires container root' >&2; exit 1; }
test_binary=$1
archive=$2
case "$(uname -m)" in
  aarch64) label=linux-arm64; digest=47c1efa081057c9c1a18e45b20cb7dd0d7d2313520d18ba7d0d35f271005fe19 ;;
  x86_64) label=linux-x86_64; digest=61a0c0f24d6b03e31bb7a73390db875ecf0de2ce53aa435eadb03d70979d79a5 ;;
  *) echo 'Unsupported native test architecture' >&2; exit 1 ;;
esac
printf '%s  %s\n' "$digest" "$archive" | sha256sum -c -
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
  libx11-6 libxi6 libxkbcommon0 libxtst6 xvfb openbox dbus-x11 \
  x11-utils python3-gi gir1.2-gtk-3.0 at-spi2-core
directory=$(mktemp -d /tmp/aice-x11-probe.XXXXXX)
trap 'rm -rf "$directory"' EXIT
chmod 755 "$directory"
tar -xzf "$archive" -C "$directory"
mkdir "$directory/home" "$directory/runtime"
chown 65534:65534 "$directory/home" "$directory/runtime"
chmod 700 "$directory/runtime"
driver=$directory/cua-driver-rs-0.29.1-$label/cua-driver
runuser -u nobody -- env -i PATH=/usr/bin:/bin HOME="$directory/home" \
  LANG=C.UTF-8 DISPLAY=:99 XDG_RUNTIME_DIR="$directory/runtime" \
  GTK_MODULES=gail:atk-bridge NO_AT_BRIDGE=0 \
  CUA_DRIVER_RS_TELEMETRY_ENABLED=false CUA_DRIVER_RS_UPDATE_CHECK=false \
  AICE_CUA_X11_CONTAINER=1 AICE_CUA_TEST_BINARY="$driver" \
  dbus-run-session -- sh -eu -c '
    Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp -ac >/dev/null 2>&1 &
    display_pid=$!
    wm_pid=
    trap '\''[ -z "$wm_pid" ] || kill "$wm_pid" 2>/dev/null || :; kill "$display_pid" 2>/dev/null || :; wait || :'\'' EXIT
    attempts=0
    until xdpyinfo >/dev/null 2>&1; do
      attempts=$((attempts + 1))
      [ "$attempts" -lt 50 ] || exit 1
      sleep 0.1
    done
    openbox >/dev/null 2>&1 &
    wm_pid=$!
    "$1" -test.run "^TestNativeLinux(BackgroundProbe|FocusSentinel)$" -test.v -test.timeout=2m
  ' sh "$test_binary"
