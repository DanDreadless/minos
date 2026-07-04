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
- **Client-facing DoT/DoH** — serve encrypted DNS with your own
  certificate; Android Private DNS ready *(shipped — manual certs;
  ACME below)*
- **Release pipeline** — CI with the race detector, tag-triggered releases
  (linux/macOS/windows binaries + checksums, multi-arch Docker on GHCR),
  and a checksum-verifying install script *(shipped)*
- **Upstream failover health** — per-upstream circuit breaker: 3 transport
  failures sidestep it, half-open probe every 30 s, always tried as last
  resort *(shipped)*
- **Private reverse zones answered locally** — RFC 6303 + CGNAT: private
  PTR lookups never leak upstream; local records and routes take
  precedence *(shipped)*
- **Query dedup + serve-stale** — concurrent identical queries collapse to
  one exchange; expired answers serve instantly (RFC 8767) with a deduped
  background refresh *(shipped)*
- **2M-domain memory budget verified** — the matcher was compacted from
  maps (~82 B/entry, 164 MB) to a sorted slab (~31 B/entry): 2M blocked
  domains now hold at 83 MB RSS against the 150 MB budget, with lookups
  still >1000× inside the latency budget *(shipped)*
- **Opt-in update check** — off by default; when enabled, one daily GET to
  the GitHub releases API surfaces "vX.Y.Z available" in the UI and CLI
  *(shipped)*
- Every setting applies live — no restart, ever, except the two listen
  addresses and query-log storage
- Single static binary, SD-card-safe storage, no telemetry

## Next up

Everything the July 2026 review flagged has shipped. What gets promoted
next comes from the list below as real-world usage decides:

## Under consideration

- ACME/Let's Encrypt automation for the DoT/DoH certificate (manual
  certs work today)
- Import through the UI (upload a gravity.db/AdGuardHome.yaml on Settings —
  the CLI importer ships today)
- Config restore (import the YAML backup through the UI)
- DNSSEC validation (today: delegate to validating DoH/DoT upstreams)

## Not building (pre-1.0)

Fixed decisions, restated from CLAUDE.md: no DHCP server, no recursive
resolver, no clustering, no plugin system. Tools that want to do
everything end up Technitium — impressive, but not what Minos is for.
Every query gets judged; the judge does not also run the post office.
