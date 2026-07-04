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
- **Migration importer** — `minos import pihole|adguard` carries over
  blocklists, allow/deny rules, local records, blocked services *(shipped)*
- **Prometheus metrics** — `/metrics` with query, cache, per-upstream, and
  per-list series; hand-rolled, scrape-only *(shipped)*
- Every setting applies live — no restart, ever, except the two listen
  addresses and query-log storage
- Single static binary, SD-card-safe storage, no telemetry

## Next up (in priority order)

### 1. Serve DoH/DoT to clients

Let phones and laptops use Minos as their encrypted resolver (Android
Private DNS, iOS profiles) so filtering follows devices onto cellular and
can't be bypassed by hardcoded DoH. The big lift is TLS certificate
management (manual certs first; ACME later) — which is why it's last in
line despite high demand.

## Under consideration

- Import through the UI (upload a gravity.db/AdGuardHome.yaml on Settings —
  the CLI importer ships today)
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
