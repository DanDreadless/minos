#!/bin/sh
# Minos one-click updater: the root half. Installed by install.sh as
# /usr/local/libexec/minos-update and started by minos-update.path when the
# Minos service (which never has root) drops a request file naming a
# release. It downloads that official release, has the binary installed now
# check its signature, checksum and that it is newer, keeps the old binary
# as minos.prev, installs, restarts, and rolls back if the new version does
# not answer within 30 seconds.
#
# Everything in the state directory is writable by the Minos service, so
# nothing read from it is trusted: the request is one validated tag, a
# symlinked request is refused, and the status file is written to a fresh
# mktemp file and renamed into place (never through a path Minos could have
# pointed somewhere else).
set -eu

REPO="DanDreadless/minos"
STATE="${MINOS_STATE_DIR:-/var/lib/minos}"
BIN="${MINOS_BIN:-/usr/local/bin/minos}"
BASE="${MINOS_RELEASE_BASE:-https://github.com/${REPO}/releases/download}"
REQ="${STATE}/update-request"
STATUS="${STATE}/update-status.json"
CONFIG="${STATE}/minos.yaml"
TAG=""
FROM=""

# status STATE MESSAGE — fixed strings only, so no JSON escaping is needed.
status() {
  t=$(mktemp "${STATE}/.update-status.XXXXXX")
  printf '{"state":"%s","version":"%s","from":"%s","message":"%s","time":%s}\n' \
    "$1" "$TAG" "$FROM" "$2" "$(date +%s)" >"$t"
  chmod 644 "$t"
  mv -f "$t" "$STATUS"
}

[ -e "$REQ" ] || exit 0
if [ -L "$REQ" ] || [ ! -f "$REQ" ]; then
  rm -f "$REQ"
  status failed "The update request was not a plain file."
  exit 1
fi
REQUESTED=$(head -c 40 "$REQ" | tr -d '\r\n ')
rm -f "$REQ"
FROM=$("$BIN" version 2>/dev/null | awk '{print $2}')
if ! printf '%s' "$REQUESTED" | grep -Eq '^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$'; then
  status failed "The update request did not name a release."
  exit 1
fi
TAG="$REQUESTED"
VERSION="${TAG#v}"

case "$(uname -m)" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  armv7l | armv8l) ARCH="armv7" ;;
  armv6l) ARCH="armv6" ;;
  *)
    status failed "This machine's architecture has no release build."
    exit 1
    ;;
esac
NAME="minos_${VERSION}_linux_${ARCH}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

status downloading "Downloading the release."
for f in "${NAME}.tar.gz" checksums.txt checksums.txt.sig; do
  if ! curl -fsSL --max-time 300 -o "${TMP}/${f}" "${BASE}/${TAG}/${f}"; then
    status failed "The download failed; nothing was changed."
    exit 1
  fi
done

status verifying "Checking the signature."
if ! "$BIN" verify-release -tag "$TAG" -dir "$TMP" -file "${NAME}.tar.gz" >/dev/null 2>"${TMP}/verify.err"; then
  status failed "The release failed its signature or checksum check; nothing was changed."
  cat "${TMP}/verify.err" >&2
  exit 1
fi
mkdir "${TMP}/x"
tar -xzf "${TMP}/${NAME}.tar.gz" -C "${TMP}/x"
NEW="${TMP}/x/${NAME}/minos"
# Archive modes are not trusted either; the version check needs to run it.
[ -f "$NEW" ] && chmod 755 "$NEW"
if [ ! -f "$NEW" ] || [ "$("$NEW" version 2>/dev/null | awk '{print $2}')" != "$VERSION" ]; then
  status failed "The downloaded binary is not the version requested; nothing was changed."
  exit 1
fi

status installing "Installing."
cp -p "$BIN" "${BIN}.prev"
install -m 755 "$NEW" "${BIN}.new"
mv -f "${BIN}.new" "$BIN"
# The systemd unit ships with the release; keep it current like install.sh.
UNIT=/etc/systemd/system/minos.service
UNIT_CHANGED=0
if [ -f "${TMP}/x/${NAME}/minos.service" ] && [ -f "$UNIT" ] && ! cmp -s "${TMP}/x/${NAME}/minos.service" "$UNIT"; then
  cp "$UNIT" "${UNIT}.bak"
  install -m 644 "${TMP}/x/${NAME}/minos.service" "$UNIT"
  systemctl daemon-reload
  UNIT_CHANGED=1
fi

# The updater refreshes itself and its units from the release too. A
# rename replaces the script without touching the copy this run is reading.
for f in minos-update.sh minos-update.service minos-update.path; do
  [ -f "${TMP}/x/${NAME}/${f}" ] || continue
  case "$f" in
    minos-update.sh) dest=/usr/local/libexec/minos-update mode=755 ;;
    *) dest=/etc/systemd/system/${f} mode=644 ;;
  esac
  install -m "$mode" "${TMP}/x/${NAME}/${f}" "${dest}.new" && mv -f "${dest}.new" "$dest"
done
systemctl daemon-reload

status restarting "Restarting Minos."
systemctl restart minos

# Healthy = the new version answering its own API (minos status reads the
# config, so it works with a password or token set).
i=0
while [ "$i" -lt 30 ]; do
  sleep 1
  i=$((i + 1))
  if "$BIN" status -config "$CONFIG" 2>/dev/null | head -1 | grep -q "^minos ${VERSION} "; then
    status done "Updated."
    exit 0
  fi
done

# Not answering: put the old binary back.
mv -f "${BIN}.prev" "$BIN"
if [ "$UNIT_CHANGED" = "1" ]; then
  mv -f "${UNIT}.bak" "$UNIT"
  systemctl daemon-reload
fi
systemctl restart minos
status rolled_back "The new version did not start, so the previous one was put back."
exit 1
