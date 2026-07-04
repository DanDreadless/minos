# Roadmap

Where Minos is headed, and why. This is the working plan for making Minos
the go-to self-hosted DNS resolver, based on a July 2026 review of the
field: Pi-hole v6, AdGuard Home, Blocky, and Technitium.

## Where Minos already stands

At or beyond parity with the field:

- Blocklist engine with hosts/plain/AdBlock formats and exact "why was this
  blocked" attribution on every entry — with a one-click pardon never more
  than two clicks away
- Per-device groups (filter/bypass/block with per-group overlays) — more
  capable than Pi-hole's groups, simpler than AdGuard's per-client rules
- Encrypted upstreams (DoH/DoT) out of the box
- **Response cache** with TTL clamps and RFC 2308 negative caching *(shipped)*
- **Local DNS records** — wildcards, CNAME chase, automatic PTR *(shipped)*
- **Conditional forwarding** to route `lan`/reverse zones at the router *(shipped)*
- **Family controls** — one-click blocked services (25-service catalog),
  per-group schedules, Safe Search enforcement *(shipped)*
- Every setting applies live — no restart, ever, except the two listen
  addresses and query-log storage
- Single static binary, SD-card-safe storage, no telemetry

## Next up (in priority order)

### 1. Pi-hole / AdGuard Home importer

`minos import pihole <dir>` (and an upload card in Settings): read
adlists, allow/deny lists, local DNS records, and groups from a Pi-hole v5
file layout or v6 database (we already ship a pure-Go SQLite driver) and
AdGuard Home YAML. Nobody else offers a real migration path; switching
cost is the #1 adoption blocker for an established Pi-hole household.

### 2. Prometheus metrics

`GET /metrics` in Prometheus text format: query/block counters, cache hit
rate, upstream latency and failures, per-list rule counts. Hand-rolled
(no new dependency), served by the existing authenticated API listener.
Scrape-only and local — consistent with the no-telemetry promise, which
is about outbound data, not your own dashboards.

### 3. Serve DoH/DoT to clients

Let phones and laptops use Minos as their encrypted resolver (Android
Private DNS, iOS profiles) so filtering follows devices onto cellular and
can't be bypassed by hardcoded DoH. The big lift is TLS certificate
management (manual certs first; ACME later) — which is why it's last in
line despite high demand.

## Under consideration

- Config restore (import the YAML backup through the UI)
- Release binaries + install script (today: build from source or Docker)
- Query deduplication (collapse concurrent identical upstream queries)
- Serve-stale (answer from expired cache while refreshing in background)
- DNSSEC validation (today: delegate to validating DoH/DoT upstreams)
- Opt-in update check (must stay opt-in: no phoning home by default)

## Not building (pre-1.0)

Fixed decisions, restated from CLAUDE.md: no DHCP server, no recursive
resolver, no clustering, no plugin system. Tools that want to do
everything end up Technitium — impressive, but not what Minos is for.
Every query gets judged; the judge does not also run the post office.
