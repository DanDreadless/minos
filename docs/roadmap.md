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
- **Service pardons** — one-click "always allow" for a whole service
  (global or per group), with the extra playback/CDN hosts streaming apps
  need; unbreaks Netflix/Disney+/Prime Video without hand-adding domains
  *(shipped)*
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
- **Notifications** — webhook + ntfy delivery for new-device, upstream
  sick/recovered, and update events; nothing sent unless configured
  *(shipped)*
- **Homelab kit** — full REST API reference, Home Assistant recipes, a
  ready-made Grafana dashboard, and 32-bit ARM builds (armv7/armv6 +
  arm/v7 Docker) for drawer Pis *(shipped)*
- **ACME automation** — Let's Encrypt certificates for DoT/DoH via the
  DNS-01 challenge (Cloudflare, deSEC, DuckDNS, RFC 2136), renewed and
  rotated live; e2e-tested against Pebble in CI *(shipped)*
- **Import & restore in the UI** — upload gravity.db/AdGuardHome.yaml to
  migrate, or restore an exported minos.yaml, from Settings *(shipped)*
- Every setting applies live — no restart, ever, except the two listen
  addresses and query-log storage
- Single static binary, SD-card-safe storage, no telemetry

## Usability & enrichment round — shipped (v0.7.x, July 2026)

A second July 2026 pass from real-world use, shipped across v0.7.0 and the
releases after it (verified complete 2026-07-11). Engineering detail lives
outside the repo (`../.claude/minos-improvements-plan.md`, now an archived
ledger). Shipped: bootstrap-free default upstream (IP-literal DoH), curated
Ferrymen picker, full-width layout + internal Docket scroll, Tribunal
drill-downs, Docket drill-downs reading the full persisted history
(`GET /api/querylog/history`, server-side filters + pagination), devices
sorted by IP, device IP → filtered Docket, gateway-first PTR enrichment with
NetBIOS and mDNS fallbacks, MAC-OUI vendor labels, MAC-based device identity
(one row and one policy per physical device across DHCP leases), service
pardons (global + per group), in-app per-install-method upgrade guidance,
rollback-safe config loading with `minos.yaml.bak` backups, and the
Svelte 5 / Vite 6 toolchain move.

### Deferred from the round (deliberate, not forgotten)

- **Config schema-version + migration seam** — deferred until a real
  migration needs it, so it ships with tolerant loading already in the field.
- **DHCP lease-file ingestion** — moot on the common home setup where the
  router (not the Minos box) runs DHCP and the lease file isn't on the Pi.
  Revisit only if users running dnsmasq/Kea beside Minos ask.
- **Install-method build stamp** — *(shipped since)* release and Docker
  builds now stamp the method via ldflags, refinable with an
  `update_install_method` config override; runtime container detection
  still wins so a release binary in a container gets Docker guidance.

## Next round — making Minos the homelab default

The implementation-ready plan is `../.claude/minos-product-plan.md`, built
from a July 2026 competitive review (Pi-hole v6, AdGuard Home, Blocky,
Technitium, NextDNS). Headlines, roughly in build order:

- **Curated blocklist catalog** on the Codex page (Hagezi / OISD /
  StevenBlack tiers, one-click subscribe — the Ferrymen-picker pattern
  applied to the harder decision)
- **Subscribed allowlists** (`action: allow` list sources — Pi-hole v6
  "antigravity" parity; imports carry AdGuard whitelist filters and v6
  antigravity lists over) *(shipped)*
- **Per-list effectiveness stats** — 7-day blocks attributed per list, so
  dead-weight lists are visible (the "why was this judged" data, aggregated)
- **Audit (dry-run) lists** — rules logged as "would block" but never
  enforced; try a strict list safely, then enforce with one click. Requested
  around Pi-hole for years, shipped by nobody
- **Bypass resistance** — Firefox DoH canary answered by default, opt-in
  iCloud Private Relay block, and an `encrypted-dns` services-catalog bundle
  blockable per group
- **Per-client dashboard** — aggregated top allowed/blocked per device,
  spanning all its IPs (AdGuard's headline advantage, matched) *(shipped)*
- **Weekly digest** through the existing webhook/ntfy notifier
- **First-run checklist** on the Tribunal

Maintainer-gated (explicit decision needed before any code): **replica
config sync** (bounded one-way push; the docs-only keepalived + API-sync
recipe can ship anytime), and **DNS-over-QUIC** (parked on the quic-go
dependency weight).

## Under consideration

- DNSSEC validation (today: delegate to validating DoH/DoT upstreams)
- DNS-over-QUIC / DoH3, client-facing and upstream (parked: needs quic-go,
  a heavy dependency against the blessed-set rule)

## Not building (pre-1.0)

Fixed decisions, restated from CLAUDE.md: no DHCP server, no recursive
resolver, no clustering, no plugin system. Tools that want to do
everything end up Technitium — impressive, but not what Minos is for.
Every query gets judged; the judge does not also run the post office.
