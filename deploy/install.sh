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
# The script is deliberately boring: no piping downloads into shells,
# everything verified against checksums.txt from the release. It also
# installs the one-click updater (minos-update.path + .service and the
# helper in /usr/local/libexec), which lets the web interface install a
# signed release later — Minos itself still never runs as root.
set -eu

REPO="DanDreadless/minos"
INSTALL_DIR="/usr/local/bin"
UNIT_DIR="/etc/systemd/system"
UNIT="${UNIT_DIR}/minos.service"
LIBEXEC="/usr/local/libexec"
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

# The one-click updater: the root helper and the path unit that starts it
# when the web interface asks. Refreshed on every run, like the main unit.
if [ -d "$UNIT_DIR" ] && [ -f "${TMP}/${NAME}/minos-update.sh" ]; then
  mkdir -p "$LIBEXEC"
  install -m 755 "${TMP}/${NAME}/minos-update.sh" "${LIBEXEC}/minos-update.new"
  mv -f "${LIBEXEC}/minos-update.new" "${LIBEXEC}/minos-update"
  install -m 644 "${TMP}/${NAME}/minos-update.service" "${UNIT_DIR}/minos-update.service"
  install -m 644 "${TMP}/${NAME}/minos-update.path" "${UNIT_DIR}/minos-update.path"
  systemctl daemon-reload 2>/dev/null || true
  systemctl enable --now minos-update.path >/dev/null 2>&1 ||
    say "note: could not enable minos-update.path; the Update button stays off"
fi

# Is a Minos running that now has a stale binary behind it?
RUNNING=0
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet minos 2>/dev/null; then
  RUNNING=1
fi

# Port 53. Minos needs it, and many systems already have something there.
# systemd-resolved's stub listener (Ubuntu, some Debian) is switched off —
# asked first when there is a terminal — in a way that keeps this machine's
# own lookups working, checked, and undone if they stop. Anything else is
# named with what to do, but never stopped from here: dnsmasq is often the
# network's DHCP server too. MINOS_KEEP_PORT53=1 skips all of this.
RESOLVED_DROPIN="/etc/systemd/resolved.conf.d/minos.conf"

ask() { # $1: question; true unless answered no. Without a terminal: yes.
  if (: </dev/tty) 2>/dev/null; then
    printf '%s [Y/n] ' "$1" >/dev/tty
    read -r answer </dev/tty || answer=""
    case "$answer" in [Nn]*) return 1 ;; esac
  fi
  return 0
}

free_resolved() {
  say ""
  say "systemd-resolved's local stub (127.0.0.53) is using port 53."
  if ! ask "Turn the stub off so Minos can use port 53? This machine keeps resolving names through systemd-resolved."; then
    say "left as it is; Minos cannot start until port 53 is free (see the getting-started guide)."
    return 0
  fi
  mkdir -p "$(dirname "$RESOLVED_DROPIN")"
  printf '# Written by the Minos installer: frees port 53 for Minos.\n[Resolve]\nDNSStubListener=no\n' >"$RESOLVED_DROPIN"
  # /etc/resolv.conf usually points at the stub, which is about to go:
  # point it at resolved's list of real upstream servers instead.
  RESOLV_BACKUP=""
  if [ -L /etc/resolv.conf ] || grep -q '127\.0\.0\.53' /etc/resolv.conf 2>/dev/null; then
    RESOLV_BACKUP="/etc/resolv.conf.minos-backup"
    cp -P /etc/resolv.conf "$RESOLV_BACKUP"
    # A bind-mounted resolv.conf (containers) cannot be replaced; the lookup
    # check below then decides whether the change stands.
    ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf 2>/dev/null ||
      { rm -f "$RESOLV_BACKUP"; RESOLV_BACKUP=""; }
  fi
  systemctl restart systemd-resolved
  sleep 1
  if getent hosts github.com >/dev/null 2>&1; then
    say "done: the stub is off and this machine still resolves names. To undo:"
    say "  sudo rm ${RESOLVED_DROPIN}${RESOLV_BACKUP:+ && sudo mv -f ${RESOLV_BACKUP} /etc/resolv.conf} && sudo systemctl restart systemd-resolved"
  else
    rm -f "$RESOLVED_DROPIN"
    if [ -n "$RESOLV_BACKUP" ]; then mv -f "$RESOLV_BACKUP" /etc/resolv.conf; fi
    systemctl restart systemd-resolved
    say "warning: this machine could not resolve names with the stub off, so it was put back as it was."
    say "         Free port 53 by hand: see the getting-started guide."
  fi
}

free_port53() {
  [ "${MINOS_KEEP_PORT53:-0}" = "1" ] && return 0
  command -v ss >/dev/null 2>&1 || return 0
  OWNERS=$(
    {
      ss -Hlnup 'sport = :53' 2>/dev/null
      ss -Hlntp 'sport = :53' 2>/dev/null
    } | grep -o 'users:(("[^"]*"' | cut -d'"' -f2 | grep -vx 'minos' | sort -u
  )
  for owner in $OWNERS; do
    case "$owner" in
      systemd-resolve*) free_resolved ;;
      dnsmasq)
        say ""
        say "dnsmasq is using port 53. If it is not also this network's DHCP server, stop it:"
        say "  sudo systemctl disable --now dnsmasq"
        ;;
      pihole-FTL)
        say ""
        say "Pi-hole is using port 53. To bring its lists and rules across, then stop it:"
        say "  sudo minos import pihole /etc/pihole -config /var/lib/minos/minos.yaml"
        say "  sudo systemctl disable --now pihole-FTL"
        ;;
      *)
        say ""
        say "${owner} is using port 53; stop it before starting Minos."
        ;;
    esac
  done
}

if [ "$RUNNING" = "0" ]; then
  free_port53
fi

say ""
if [ "$RUNNING" = "1" ] && [ "$PREV" = "$("${INSTALL_DIR}/minos" version)" ] && [ "$UNIT_CHANGED" = "0" ]; then
  say "already up to date — nothing to restart."
elif [ "$RUNNING" = "1" ] && [ "$RESTART" = "1" ]; then
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
  say "If anything above says port 53 is still in use, free it first — the walkthrough:"
  say "  https://github.com/${REPO}/blob/main/docs/getting-started.md"
else
  say "done. See https://github.com/${REPO}/blob/main/docs/getting-started.md to set up."
fi
