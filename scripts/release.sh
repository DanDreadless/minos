#!/usr/bin/env bash
# Cut a Minos release on both platforms.
#
# Gitea (vault-tec.local) is the source of truth; GitHub is a public
# mirror whose runners build the artifacts — Gitea itself is a NAS
# container and never runs CI. This script drives the whole flow:
#
#   verify locally → tag → push to Gitea → mirror to GitHub → wait for
#   the GitHub build → verify checksums → publish the same assets on
#   Gitea, so both platforms carry a complete release.
#
# Usage:
#   scripts/release.sh v0.19.1                 # full flow
#   scripts/release.sh --assets-only v0.19.0   # re-sync assets to Gitea
#   scripts/release.sh --skip-verify v0.19.1   # checks already run
#   scripts/release.sh --notes-file notes.md v0.19.1
#
# Environment:
#   GITEA_TOKEN   required — Gitea access token with repository write
#   GITHUB_TOKEN  required unless a Gitea push mirror is configured;
#                 needs `repo` AND `workflow` scope (pushes touch
#                 .github/workflows/)
#   GITEA_URL     default http://vault-tec.local:3000
set -euo pipefail

GITEA_URL="${GITEA_URL:-http://vault-tec.local:3000}"
GITEA_REPO="${GITEA_REPO:-dreadless/minos}"
GITHUB_REPO="${GITHUB_REPO:-DanDreadless/minos}"
BUILD_TIMEOUT_MIN="${BUILD_TIMEOUT_MIN:-30}"

# Must match the target matrix in .github/workflows/release.yml.
TARGETS=(
  linux_amd64 linux_arm64 linux_armv7 linux_armv6
  darwin_amd64 darwin_arm64 windows_amd64
)

assets_only=0
skip_verify=0
notes_file=""
tag=""
while [ $# -gt 0 ]; do
  case "$1" in
    --assets-only) assets_only=1 ;;
    --skip-verify) skip_verify=1 ;;
    --notes-file) notes_file="${2:?--notes-file needs a path}"; shift ;;
    -h|--help) sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    *) tag="$1" ;;
  esac
  shift
done

die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[ -n "$tag" ] || die "usage: scripts/release.sh [--assets-only] [--skip-verify] vX.Y.Z"
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "tag must look like v1.2.3, got '$tag'"
version="${tag#v}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Local secrets live in a git-ignored .env (see .env.example). Anything
# already exported wins, so CI or a one-off override still works.
if [ -f "$repo_root/.env" ]; then
  saved_gitea="${GITEA_TOKEN:-}" saved_github="${GITHUB_TOKEN:-}"
  set -a; . "$repo_root/.env"; set +a
  [ -n "$saved_gitea" ] && GITEA_TOKEN="$saved_gitea"
  [ -n "$saved_github" ] && GITHUB_TOKEN="$saved_github"
fi
: "${GITEA_TOKEN:?set GITEA_TOKEN (Gitea token with repository write) or put it in .env}"

gitea_api() {
  local method="$1" path="$2"; shift 2
  curl -sS -f -X "$method" \
    -H "Authorization: token $GITEA_TOKEN" \
    "$@" "$GITEA_URL/api/v1/repos/$GITEA_REPO$path"
}

# first_json_id pulls the id out of a Gitea release object. Gitea puts
# "id" first in the payload; the numeric guard below catches any change.
first_json_id() { grep -o '"id":[0-9]*' | head -1 | cut -d: -f2; }

if [ "$assets_only" -eq 0 ]; then
  say "preflight"
  [ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || die "not on main"
  [ -z "$(git status --porcelain)" ] || die "working tree is dirty"
  git rev-parse -q --verify "refs/tags/$tag" >/dev/null &&
    die "tag $tag already exists (use --assets-only to re-sync)"
  git fetch -q origin main
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
    die "main differs from origin/main — push or pull first"

  if [ "$skip_verify" -eq 0 ]; then
    say "verifying (gofmt, go test, lint, frontend)"
    # gofmt against the *committed blobs*, which is what CI sees: a
    # CRLF working tree makes `gofmt -l .` flag everything, and
    # golangci-lint has missed a real problem here before (a UTF-8 BOM
    # from PowerShell's Set-Content).
    fmt_tmp="$(mktemp -d)"
    for f in $(git ls-files '*.go'); do
      mkdir -p "$fmt_tmp/$(dirname "$f")"
      git show "HEAD:$f" > "$fmt_tmp/$f"
    done
    unformatted="$(gofmt -l "$fmt_tmp" | sed "s|$fmt_tmp/||")"
    rm -rf "$fmt_tmp"
    [ -z "$unformatted" ] || die "gofmt needed on: $unformatted"

    # -race needs cgo, which the Windows dev box lacks; the GitHub run
    # is the authoritative race check and blocks the release there.
    go test ./...
    if command -v golangci-lint >/dev/null 2>&1; then
      golangci-lint run
    elif [ -x "$HOME/go/bin/golangci-lint" ]; then
      "$HOME/go/bin/golangci-lint" run
    else
      echo "warning: golangci-lint not found, skipping lint" >&2
    fi
    (cd web && npx --yes svelte-check --output human >/dev/null && npm run build >/dev/null)
    [ -z "$(git status --porcelain web/dist)" ] ||
      die "web/dist changed during build — commit the rebuilt bundle first"
  fi

  say "tagging $tag"
  git tag -a "$tag" -m "Minos $tag"
  say "pushing to Gitea (source of truth)"
  git push origin main "$tag"

  # A configured push mirror replicates the tag on its own; pushing
  # explicitly is harmless (it becomes a no-op) and covers the case
  # where no mirror is set up yet.
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    say "mirroring to GitHub"
    git push -q "https://x-access-token:$GITHUB_TOKEN@github.com/$GITHUB_REPO.git" main "$tag"
  else
    say "no GITHUB_TOKEN — relying on the Gitea push mirror"
  fi
fi

say "waiting for the GitHub build to publish assets (up to ${BUILD_TIMEOUT_MIN}m)"
base_url="https://github.com/$GITHUB_REPO/releases/download/$tag"
deadline=$(( $(date +%s) + BUILD_TIMEOUT_MIN * 60 ))
until curl -sSfL -o /dev/null "$base_url/checksums.txt" 2>/dev/null; do
  [ "$(date +%s)" -lt "$deadline" ] || die "GitHub assets never appeared for $tag"
  sleep 30
done

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
say "downloading assets"
curl -sSfL -o "$workdir/checksums.txt" "$base_url/checksums.txt"
for t in "${TARGETS[@]}"; do
  ext="tar.gz"; case "$t" in windows_*) ext="zip" ;; esac
  name="minos_${version}_${t}.${ext}"
  curl -sSfL -o "$workdir/$name" "$base_url/$name" || die "missing asset $name"
done

say "verifying checksums"
(cd "$workdir" && sha256sum -c checksums.txt) ||
  die "checksum mismatch — do not publish these assets"

say "publishing the Gitea release"
if existing="$(gitea_api GET "/releases/tags/$tag" 2>/dev/null)"; then
  release_id="$(printf '%s' "$existing" | first_json_id)"
  say "reusing existing Gitea release $release_id"
else
  if [ -n "$notes_file" ]; then
    body="$(cat "$notes_file")"
  else
    prev="$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)"
    changes="$(git log --no-merges --pretty='* %s' ${prev:+"$prev..$tag"} | head -40)"
    body="$(printf '## Changes\n\n%s\n\nBinaries, checksums, and container images are also on the GitHub mirror: https://github.com/%s/releases/tag/%s' \
      "$changes" "$GITHUB_REPO" "$tag")"
  fi
  # Build the JSON with python to get the escaping right.
  payload="$(python -c 'import json,sys; print(json.dumps({"tag_name":sys.argv[1],"name":"Minos "+sys.argv[1],"body":sys.argv[2]}))' \
    "$tag" "$body")"
  release_id="$(printf '%s' "$payload" | gitea_api POST "/releases" \
    -H "Content-Type: application/json" --data-binary @- | first_json_id)"
fi
[[ "$release_id" =~ ^[0-9]+$ ]] || die "could not determine the Gitea release id"

have="$(gitea_api GET "/releases/$release_id/assets" | grep -o '"name":"[^"]*"' | cut -d'"' -f4 || true)"
for f in "$workdir"/*; do
  name="$(basename "$f")"
  if printf '%s\n' "$have" | grep -qx "$name"; then
    echo "    $name already attached, skipping"
    continue
  fi
  echo "    uploading $name"
  gitea_api POST "/releases/$release_id/assets?name=$name" -F "attachment=@$f" >/dev/null
done

say "done"
echo "  Gitea:  $GITEA_URL/$GITEA_REPO/releases/tag/$tag"
echo "  GitHub: https://github.com/$GITHUB_REPO/releases/tag/$tag"
