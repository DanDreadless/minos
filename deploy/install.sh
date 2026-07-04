#!/bin/sh
# Minos installer: downloads the latest release binary for this machine,
# verifies its checksum, and installs it to /usr/local/bin. Optionally
# installs the systemd unit. Linux only (amd64/arm64); for other platforms
# grab an archive from the releases page.
#
#   curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh
#
# The script is deliberately boring: no piping downloads into shells, no
# self-updates, everything verified against checksums.txt from the release.
set -eu

REPO="DanDreadless/minos"
INSTALL_DIR="/usr/local/bin"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "this installer supports Linux only — see https://github.com/${REPO}/releases for other platforms"
[ "$(id -u)" = "0" ] || die "run as root (installs to ${INSTALL_DIR}): curl ... | sudo sh"

case "$(uname -m)" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) die "unsupported architecture $(uname -m) (amd64 and arm64 builds are published)" ;;
esac

command -v curl >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

say "finding the latest release..."
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
  grep -m1 '"tag_name"' | cut -d'"' -f4)
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
install -m 755 "${TMP}/${NAME}/minos" "${INSTALL_DIR}/minos"
say "installed ${INSTALL_DIR}/minos ($("${INSTALL_DIR}/minos" version))"

# Offer the systemd unit when systemd is present and the unit isn't yet.
if [ -d /etc/systemd/system ] && [ ! -f /etc/systemd/system/minos.service ]; then
  install -m 644 "${TMP}/${NAME}/minos.service" /etc/systemd/system/minos.service
  systemctl daemon-reload
  say ""
  say "systemd unit installed. To start Minos now and on every boot:"
  say "  sudo systemctl enable --now minos"
  say ""
  say "Before first start, make sure port 53 is free — the walkthrough:"
  say "  https://github.com/${REPO}/blob/main/docs/getting-started.md"
else
  say "done. See https://github.com/${REPO}/blob/main/docs/getting-started.md to set up."
fi
