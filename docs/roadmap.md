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

## Next up — usability & enrichment round (planned July 2026)

A second July 2026 pass from real-world use. Detailed engineering plan lives
outside the repo (`../.claude/minos-improvements-plan.md`); highlights:

- **Bootstrap-free default upstream** — ship `https://1.1.1.1/dns-query`
  instead of `cloudflare-dns.com`, removing the circular dependency where a
  DNS server must resolve its own resolver's hostname first. Cloudflare's
  cert carries the IP as a SAN, so TLS validation still passes with zero
  bootstrap code. *(planned)*
- **Curated Ferrymen picker** — choose primary/secondary upstreams from a
  known-good resolver list (Cloudflare, Quad9, Google, Mullvad, AdGuard,
  OpenDNS — all IP-literal DoH/DoT), custom entries still allowed. *(planned)*
- **Better device identity** — hostnames don't resolve in production because
  PTR lookups loop back into Minos's own private-reverse backstop (NXDOMAIN).
  **Router-directed PTR** now sends reverse lookups to the LAN gateway (which
  knows DHCP names) before the system resolver, off the hot path *(shipped)*.
  Further layers remain, all off-hot-path: DHCP lease-file ingestion
  (read-only — still no DHCP server), mDNS/NetBIOS fallbacks, and a MAC-OUI
  vendor label so every device is identifiable even without a name.
  *(planned)*
- **Tribunal drill-downs** — click the Condemned tile to jump to the blocked
  Docket; click a busiest client to see its allowed/condemned domains.
  *(planned)*
- **UI polish** — content fills the full width beside the sidebar on every
  page (mobile-aware); the Docket table scrolls internally instead of the
  whole page; devices sorted by IP so rows stop jumping. *(planned)*
- **Codex/family-controls reconciliation** — per-group service blocking and
  schedules already exist (via groups on the Devices page); align the Codex
  page copy and docs with that model instead of implying per-device/per-time
  controls live on the Codex page. Global service schedules and group-less
  per-device blocks are separate, maintainer-gated follow-ups. *(planned)*
- **Settings-safe upgrades** — make the opt-in "new version available" notice
  actionable without ever losing settings, and keep the installer's "no
  self-updates, deliberately boring" promise: Minos never replaces its own
  binary. Config and history already live outside the binary (StateDirectory
  / Docker volume), so an upgrade preserves them; the work is making rollback
  safe (today an older binary refuses a newer config's unknown fields), taking
  a pre-change config backup, and adding a config version/migration seam. The
  notice then shows the **right upgrade command for how this instance was
  installed** — quick-install/binary (re-run the checksum-verified installer
  + `systemctl restart`), Docker (`compose pull && up -d`), or build-from-
  source (`git checkout` the tag + rebuild) — detected from a build-time stamp
  refined by runtime Docker/systemd checks, with a "What's new" link.
  Display-only guidance; Minos runs nothing itself. *(planned)*

What gets promoted after that comes from the list below as real-world usage
decides:

## Under consideration

- DNSSEC validation (today: delegate to validating DoH/DoT upstreams)

## Not building (pre-1.0)

Fixed decisions, restated from CLAUDE.md: no DHCP server, no recursive
resolver, no clustering, no plugin system. Tools that want to do
everything end up Technitium — impressive, but not what Minos is for.
Every query gets judged; the judge does not also run the post office.
