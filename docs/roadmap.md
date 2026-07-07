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

## Usability & enrichment round — shipped in v0.7.0 (July 2026)

A second July 2026 pass from real-world use, released as **v0.7.0**. Detailed
engineering plan lives outside the repo
(`../.claude/minos-improvements-plan.md`). Shipped: bootstrap-free default
upstream (IP-literal DoH), curated Ferrymen picker (cert-verified known-good
resolvers), full-width layout + internal Docket scroll, Tribunal drill-downs,
device list sorted by IP, tidier import UI, gateway-directed PTR enrichment,
Codex/family-controls doc reconciliation, and rollback-safe config loading
with a `minos.yaml.bak` backup. The frontend toolchain also moved to
Svelte 5 / Vite 6, clearing all open Dependabot alerts.

### Still planned (carried over from the round)

- **Drill-downs read persisted history (Docket history)** — *known bug in the
  v0.7.0 drill-downs.* The Docket's data source (`GET /api/querylog` →
  `querylog.Recent()`) reads only the in-memory ring buffer, which is empty
  after a restart and refills from live traffic; the dashboard aggregates
  (busiest clients, top blocked) read SQLite, i.e. the full 90-day history. So
  clicking a busiest client that shows 3000+ condemned lands on a Docket
  showing only events since the last restart — a mismatch that *looks* like
  data loss after an upgrade but isn't (SQLite keeps everything; don't reset
  the aggregates). Fix: wire persisted history with server-side filters.
  `querylog.QueryHistory()` already reads SQLite paginated by timestamp but is
  unused by the API — extend it to accept `client` / `qname` / `verdict`
  filters, add `GET /api/querylog/history?client=&qname=&verdict=&before=&limit=`,
  and have the Docket load from it when opened via a drill-down or a search
  (with "load older" pagination), keeping the live WebSocket stream prepending
  new matches on top. SQLite reads stay off the query hot path (as the
  aggregates already are); never flush per query. *(planned, next)*
- **In-app upgrade guidance** — the "new version available" notice currently
  only links to the GitHub release; it should show the **exact upgrade
  command for how this instance was installed**: quick-install/binary →
  re-run the checksum-verified installer + `systemctl restart`; Docker →
  `compose pull && up -d`; build-from-source → `git checkout` the tag +
  rebuild. Detect the method from a build-time stamp refined by runtime
  Docker/systemd checks, with a "What's new" link. Display-only — Minos never
  replaces its own binary (the installer's deliberate "no self-updates" stance
  stands). Manual steps are documented in `getting-started.md` meanwhile.
- **Further device identity** *(next up)* — a production check settled the
  direction: the LAN router returns NXDOMAIN for reverse (PTR) lookups, so
  gateway-directed PTR yields nothing on networks like it, and DHCP lease-file
  ingestion is moot when the router (not Minos) is the DHCP server — the lease
  file isn't on the Pi. Real names have to come from the devices themselves.
  **Hard constraint: all of this stays on the enrichment worker and must never
  add latency to a DNS request** — the query hot path is untouched.
  - **MAC → vendor labels (OUI)** — a small embedded OUI-prefix table turns
    the ARP-derived MAC into a vendor (Apple, Google, Amazon, Raspberry Pi,
    Espressif…). Add a **Vendor column** to the Devices table so every device
    is identifiable even when no hostname resolves; works for 100% of devices
    with a known MAC, needs no network cooperation. The universal baseline.
    *(shipped)*
  - **mDNS reverse lookup** — reverse PTR to `224.0.0.251:5353` (unicast-
    response bit) resolves `.local` names for Apple / Fire TV / LG / printer /
    IoT devices, and is the one source that works when the router won't do
    PTR. Sent on every interface, so multi-homed hosts still reach the LAN.
    *(shipped)*
  - **NetBIOS / NBSTAT** — the layer for Windows / Samba machine names, which
    typically run no mDNS responder and would otherwise stay blank. A single
    unicast NBSTAT node-status query to UDP 137 reads the device's own name
    table; it slots into the hostname chain after unicast PTR and before mDNS
    (cheap unicast, fast-fail on non-Windows hosts). Stdlib-only, on the
    enrichment worker, never on the query path. *(shipped)*

  All best-effort and layered (first hit wins for the hostname; the vendor
  label is always computed from the MAC); a device with none simply shows its
  vendor or bare IP.
- **Devices drill-down to the Docket** — click a device's IP on the Devices
  page to open the Docket filtered to that client (all its allowed and denied
  queries), mirroring the Tribunal's busiest-client drill-down. Reuses the
  existing `docketHref`/persisted-history plumbing; a frontend-only link.
  *(shipped)*
- **MAC-based device identity** — a device is identified by its MAC when one
  is known (else its IP), so a power-cycled box that grabs a new DHCP lease
  shows as one row, not a duplicate: every IP it has used folds into a single
  Devices entry (counts summed, drill-down spanning them all). Group/block
  assignments follow the MAC across leases too — the hot-path policy table
  stays IP-keyed but is rebuilt from MAC assignments off the query path, and a
  MAC-keyed client keeps a valid last-known IP so config still loads on an
  older binary (no new YAML key). Falls back to IP for devices Minos can't see
  at layer 2 (off-subnet, IPv6, DoT/DoH). *(shipped)*
- **Config schema-version + migration seam** — deferred until a real
  migration needs it, so it ships with tolerant loading already in the field.

Beyond these, what gets promoted comes from the list below as real-world
usage decides:

## Under consideration

- DNSSEC validation (today: delegate to validating DoH/DoT upstreams)

## Not building (pre-1.0)

Fixed decisions, restated from CLAUDE.md: no DHCP server, no recursive
resolver, no clustering, no plugin system. Tools that want to do
everything end up Technitium — impressive, but not what Minos is for.
Every query gets judged; the judge does not also run the post office.
