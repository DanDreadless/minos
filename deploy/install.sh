#!/bin/sh
# Minos installer: downloads the latest release binary for this machine,
# verifies its checksum, and installs it to /usr/local/bin. Also installs
# the systemd unit, and keeps it current on later runs. Linux only
# (amd64/arm64/armv7/armv6); for other platforms grab an archive from the
# releases page.
#
#   curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh
#
# Upgrades: the new binary lands, but a running Minos keeps serving the old
# one until it restarts. The script says so, loudly, rather than restarting
# your network's resolver behind your back. To have it restart for you:
#
#   curl -fsSL .../install.sh | sudo MINOS_RESTART=1 sh
#
# The script is deliberately boring: no piping downloads into shells, no
# self-updates, everything verified against checksums.txt from the release.
set -eu

REPO="DanDreadless/minos"
INSTALL_DIR="/usr/local/bin"
UNIT_DIR="/etc/systemd/system"
UNIT="${UNIT_DIR}/minos.service"
RESTART="${MINOS_RESTART:-0}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "this installer supports Linux only — see https://github.com/${REPO}/releases for other platforms"
[ "$(id -u)" = "0" ] || die "run as root (installs to ${INSTALL_DIR}): curl ... | sudo sh"

case "$(uname -m)" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  armv7l | armv8l) ARCH="armv7" ;; # 32-bit Pi OS on Pi 2/3/4
  armv6l) ARCH="armv6" ;;          # Pi Zero / Pi 1
  *) die "unsupported architecture $(uname -m) (amd64, arm64, armv7, and armv6 builds are published)" ;;
esac

command -v curl >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

say "finding the latest release..."
# No -m1 on the grep: an early exit closes the pipe while curl is still
# writing the (ever-growing) release JSON, and curl then prints a spurious
# "(23) Failure writing output" even though the tag was captured fine.
# /releases/latest carries exactly one tag_name, so a full read is identical.
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
  grep '"tag_name"' | cut -d'"' -f4)
[ -n "$TAG" ] || die "could not determine the latest release tag"
VERSION="${TAG#v}"
NAME="minos_${VERSION}_linux_${ARCH}"
BASE="https://github.com/${REPO}/releases/download/${TAG}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

say "downloading ${NAME}.tar.gz (${TAG})..."
curl -fsSL -o "${TMP}/${NAME}.tar.gz" "${BASE}/${NAME}.tar.gz"
curl -fsSL -o "${TMP}/checksums.txt" "${BASE}/checksums.txt"

say "verifying checksum..."
# Tolerate an optional ./ prefix in checksums.txt (v0.1.0 has one), and
# rewrite to the bare local filename before checking.
(cd "$TMP" &&
  grep -E "  (\./)?${NAME}\.tar\.gz\$" checksums.txt |
  sed 's|  \./|  |' | sha256sum -c - >/dev/null) ||
  die "checksum verification failed — aborting without installing"

tar -xzf "${TMP}/${NAME}.tar.gz" -C "$TMP"

# What was here before, if anything. An upgrade needs a restart to take
# effect and a fresh install needs setting up — different messages.
PREV=""
if [ -x "${INSTALL_DIR}/minos" ]; then
  PREV=$("${INSTALL_DIR}/minos" version 2>/dev/null || true)
fi

install -m 755 "${TMP}/${NAME}/minos" "${INSTALL_DIR}/minos"
say "installed ${INSTALL_DIR}/minos ($("${INSTALL_DIR}/minos" version))"

# The systemd unit, kept current. Installed when absent, and refreshed when
# it differs from the one this release ships — the old script only ever
# wrote it once, so a changed unit could never reach an existing install. An
# unchanged unit is left alone, and a replaced one is backed up first: it
# may have been hand-edited, and .bak is the recovery point (the same
# contract the config file's .bak offers).
UNIT_CHANGED=0
if [ -d "$UNIT_DIR" ]; then
  if [ ! -f "$UNIT" ]; then
    install -m 644 "${TMP}/${NAME}/minos.service" "$UNIT"
    systemctl daemon-reload 2>/dev/null || true
    UNIT_CHANGED=1
  elif ! cmp -s "${TMP}/${NAME}/minos.service" "$UNIT"; then
    cp "$UNIT" "${UNIT}.bak"
    install -m 644 "${TMP}/${NAME}/minos.service" "$UNIT"
    systemctl daemon-reload 2>/dev/null || true
    UNIT_CHANGED=1
    say "systemd unit updated (previous saved to ${UNIT}.bak)"
  fi
fi

# Is a Minos running that now has a stale binary behind it?
RUNNING=0
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet minos 2>/dev/null; then
  RUNNING=1
fi

say ""
if [ "$RUNNING" = "1" ] && [ "$RESTART" = "1" ]; then
  say "restarting minos..."
  systemctl restart minos
  say "restarted — now running $("${INSTALL_DIR}/minos" version)"
elif [ "$RUNNING" = "1" ]; then
  say "=============================================================="
  say " Minos is still serving the OLD binary${PREV:+ ($PREV)}."
  say " The new one takes effect on restart:"
  say ""
  say "   sudo systemctl restart minos"
  say ""
  say " (or re-run this installer with MINOS_RESTART=1)"
  say "=============================================================="
elif [ "$UNIT_CHANGED" = "1" ] && [ -z "$PREV" ]; then
  say "systemd unit installed. To start Minos now and on every boot:"
  say "  sudo systemctl enable --now minos"
  say ""
  say "Before first start, make sure port 53 is free — the walkthrough:"
  say "  https://github.com/${REPO}/blob/main/docs/getting-started.md"
else
  say "done. See https://github.com/${REPO}/blob/main/docs/getting-started.md to set up."
fi
