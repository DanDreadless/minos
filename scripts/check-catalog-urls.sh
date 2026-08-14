#!/usr/bin/env bash
# Probe every URL in the curated blocklist catalog.
#
# A dead URL in web/src/lib/blocklists.ts is a broken one-click subscribe
# for every user, and publishers reshuffle without notice — Hagezi
# deleted its whole `domains/` tree in August 2026, silently breaking
# three catalog entries until a user reported it. This is the check that
# should have caught it.
#
# Usage: scripts/check-catalog-urls.sh [--verbose]
# Exits non-zero if any URL does not answer 200.
set -uo pipefail

catalog="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/web/src/lib/blocklists.ts"
[ -f "$catalog" ] || { echo "catalog not found: $catalog" >&2; exit 2; }

verbose=0
[ "${1:-}" = "--verbose" ] && verbose=1

urls="$(grep -o "url: '[^']*'" "$catalog" | cut -d"'" -f2)"
[ -n "$urls" ] || { echo "no URLs found in $catalog" >&2; exit 2; }

failed=0
count=0
while IFS= read -r url; do
  count=$((count + 1))
  # HEAD is enough and avoids pulling megabytes; every publisher in the
  # catalog answers it. Two attempts, so one flaky CDN response doesn't
  # cry wolf.
  code=""
  for attempt in 1 2; do
    code="$(curl -sS -o /dev/null -w '%{http_code}' -I -L --max-time 45 "$url" 2>/dev/null)"
    [ "$code" = "200" ] && break
    [ "$attempt" = "1" ] && sleep 5
  done
  if [ "$code" = "200" ]; then
    [ "$verbose" -eq 1 ] && echo "ok    $url"
  else
    echo "FAIL  ${code:-000}  $url"
    failed=$((failed + 1))
  fi
done <<< "$urls"

if [ "$failed" -gt 0 ]; then
  echo
  echo "$failed of $count catalog URLs are not reachable." >&2
  echo "Find the publisher's current path, verify it parses with no skipped" >&2
  echo "rules, then update web/src/lib/blocklists.ts and rebuild web/dist." >&2
  exit 1
fi
echo "all $count catalog URLs OK"
