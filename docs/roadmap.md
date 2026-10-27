# Roadmap

Where Minos is headed, and why. This is the working plan for making Minos
the go-to self-hosted DNS resolver, based on a July 2026 review of the
field: Pi-hole v6, AdGuard Home, Blocky, and Technitium.

## Where Minos already stands

At or beyond parity with the field:

- Blocklist engine with hosts/plain/AdBlock formats and exact "why was this
  blocked" attribution on every entry — with a one-click allow never more
  than two clicks away
- Per-device groups (filter/bypass/block with per-group overlays) — more
  capable than Pi-hole's groups, simpler than AdGuard's per-client rules
- Encrypted upstreams (DoH/DoT) out of the box
- **Response cache** with TTL clamps and RFC 2308 negative caching *(shipped)*
- **Local DNS records** — wildcards, CNAME chase, automatic PTR *(shipped)*
- **Conditional forwarding** to route `lan`/reverse zones at the router *(shipped)*
- **Family controls** — one-click blocked services (57-service catalog),
  per-group schedules, Safe Search enforcement *(shipped)*
- **Allowed services** — one-click "always allow" for a whole service
  (global or per group), with the extra playback/CDN hosts streaming apps
  need; unbreaks Netflix/Disney+/Prime Video without hand-adding domains
  *(shipped)*
- **Custom services** — define your own named domain bundle and block or
  allow it like any catalog service, globally or per group *(shipped)*
- **Device pages** — a detail page per physical device: identity, every
  IP it has held, persistent notes, activity windows up to 90 days, and
  its full retained query history across all its addresses *(shipped)*
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
  resort; surfaced in Settings as a health light per upstream resolver, with
  per-upstream counts on hover
  *(shipped)*
- **Private reverse zones answered locally** — RFC 6303 + CGNAT: private
  PTR lookups never leak upstream; local records and routes take
  precedence *(shipped)*
- **Query dedup + serve-stale** — concurrent identical queries collapse to
  one exchange; expired answers serve instantly (RFC 8767) with a deduped
  background refresh *(shipped)*
- **DNSSEC validation** — in-process chain-of-trust validation
  (RRSIG/DNSKEY/DS to the root anchors, NSEC/NSEC3 denial proofs,
  KeyTrap-bounded), `dns.dnssec: off | permissive | enforce`: permissive
  counts without blocking, enforce SERVFAILs forged answers with Query Log
  attribution, and unjudgeable answers always pass so a non-DNSSEC
  upstream can never break resolution. Neither Pi-hole nor AdGuard Home
  validates in-process — this is a differentiator *(shipped, August 2026)*
- **DNSSEC visibility** — permissive mode is only useful if you can read
  it, so a failed answer it lets through is now recorded in the Query Log the
  way an audit list is, and the Dashboard answers the question permissive
  exists for: *"N answers would have been refused in enforce mode"*, with
  the domains, linked. The card also flags the quiet failure where an
  upstream returns no DNSSEC records at all and validation is doing
  nothing — previously indistinguishable from everything being fine
  *(shipped, August 2026)*
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
resolver picker, full-width layout + internal Query Log scroll, Dashboard
drill-downs, Query Log drill-downs reading the full persisted history
(`GET /api/querylog/history`, server-side filters + pagination), devices
sorted by IP, device IP → filtered Query Log, gateway-first PTR enrichment with
NetBIOS and mDNS fallbacks, MAC-OUI vendor labels, MAC-based device identity
(one row and one policy per physical device across DHCP leases), allowed
services (global + per group), in-app per-install-method upgrade guidance,
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

## Homelab round — shipped (v0.13.0–v0.14.0, July 2026)

The round that made Minos the homelab default candidate, from a July 2026
competitive review (Pi-hole v6, AdGuard Home, Blocky, Technitium, NextDNS).
The engineering plan is archived outside the repo
(`../.claude/minos-product-plan.md`); all nine items shipped, verified
complete 2026-07-12:

- **Curated blocklist catalog** — Hagezi / OISD / StevenBlack / threat-feed
  tiers on the Blocklists page, every URL verified through the real fetch/parse
  pipeline, one-click subscribe
- **Subscribed allowlists** — `action: allow` sources; Pi-hole v6
  "antigravity" parity, importer-aware
- **Per-list effectiveness stats** — 7-day blocks attributed per list, so
  dead-weight lists are visible
- **Audit (dry-run) lists** — rules logged as amber "would block", never
  enforced; one click to enforce. Requested around Pi-hole for years,
  shipped by nobody
- **Bypass resistance** — Firefox DoH canary (default on), opt-in iCloud
  Private Relay block, blockable `encrypted-dns` service bundle
- **Per-client dashboard** — top allowed/blocked per device across all its
  IPs
- **Traffic digest** — opt-in daily/weekly summary through webhook/ntfy
- **First-run checklist** — the Dashboard walks a fresh install to a
  working, blocking resolver
- **Install-method build stamp** — release/Docker builds stamp upgrade
  guidance

## Catalog — shipped (August 2026)

- **The catalog is verified by compiling it** — every curated list is
  fetched and run through the real parser weekly in CI, asserting it
  parses, skips nothing, and matches its advertised size. The previous
  HEAD probe caught only deletions; this catches a list that was
  repurposed, emptied, reformatted, or has drifted. Its first run found a
  parser bug and a 47% size-hint error.
- **Bypass and Family categories** — a DNS Bypass Blocker (public DoH,
  VPN and proxy endpoints — a device using them leaves Minos entirely and
  ignores every other list) and an adult-content list under a new Family
  tier, the parental-filtering gap every comparable resolver fills.
## Host health — shipped (August 2026)

- **The Dashboard reports the machine Minos runs on** — CPU, load, memory,
  disk, temperature, uptime and Minos's own footprint, plus Prometheus
  gauges. The disk figure is the query log's filesystem (the SD card on a
  Pi) and warns past 90%, because a full disk stops Minos recording.
- Readings the platform cannot measure show an em dash, never a zero, and
  **container installs report nothing at all** — inside a container the
  host's figures describe a machine Minos does not have to itself and the
  container's limits are not the host, so neither answer is worth showing.

## Recent polish — shipped (v0.18.1–v0.18.2, July 2026)

Small UX corrections from real-world use, refining the shipped feature set
above rather than adding capability:

- **Upstream health lights** — green (answering), red (failing), and since
  October 2026 a hollow ring for a backup that has not been tested yet. The
  July two-state version painted idle backups green, which hid one that had
  never answered a query for 49 days.
- **Themed scrollbars** — track sinks into the page background and the thumb
  is a muted laurel gold that brightens on hover, matching the app's accent
  instead of the browser-default grey.
- **Themed dropdowns** (v0.18.2) — select popups render dark, with the
  highlighted row on the same grey as the nav and table rows rather than the
  OS blue selection bar.

## Next round — naming the unnamed (device identity)

Too many Devices rows still show no hostname or vendor. The
implementation-ready plan is `../.claude/minos-device-identity-plan.md`:
passive-first identity gathering with zero query-path cost. Tier 1 is
built (July 2026): the full IEEE OUI registry as a compact slab plus
honest "Private address" labels for randomized MACs *(shipped)*, IPv6
neighbour-table MAC-tagging *(shipped)*, provenance & precedence on every
identity field *(shipped)*, deeper mDNS — direct queries, passive
announcement listener, `_device-info` models *(shipped)*, SSDP/UPnP
friendly names *(shipped)*, a passive DHCP broadcast listener —
explicitly a listener, never a DHCP server *(shipped)*, and
traffic-pattern OS hints as a clearly-labelled last resort *(shipped)*.

Maintainer-gated (explicit decision needed before any code): **replica
config sync** (bounded one-way push; the docs-only keepalived + API-sync
recipe can ship anytime).

**Declined: router-assisted identity** (October 2026). Probed against a
TP-Link Deco BE68: no PTR answers for LAN devices, SNMP closed, and UPnP is
MiniUPnPd IGD only (WAN/port-forwarding services, no host list). The device
list exists only behind TP-Link's undocumented, encrypted web login — it
would mean storing the router's admin password and tracking a private
protocol across firmware updates, for names that labelling devices once, or
the DHCP listener over time, already provides. Standard protocols only.

## Best-in-class round — shipped (v0.21.3–v0.31.0, October 2026)

A full audit against Pi-hole v6, AdGuard Home, Technitium and Blocky, and
the work it led to:

- **Plain names** — the themed labels (Tribunal, Docket, Codex, Ferrymen…)
  retired for plain words; Minos keeps its name, logo and tagline
- **Reliable blocklists** — bodies kept on disk and used at boot,
  conditional refreshes, classified failures with quick retries, a
  shrink guard against bad publishes, publisher mirrors, and alerts when a
  list stops updating or is retired
- **Fast search and stats** — a domain dictionary makes Query Log search
  answer in milliseconds on millions of rows; exact rollups make 90-day
  dashboards fast; SQLite memory capped (`querylog.memory_mb`)
- **Application log in the UI** — live, filterable, secrets redacted
- **Reorganised interface** — status, pause and Check a domain in the side
  menu, a Needs attention panel, tabbed Settings with unsaved-changes
  protection, Blocking split into Blocklists / Allowed / Blocked, and every
  page reflowing to a phone
- **Help built in** — every page and setting documented in the app, with a
  test that fails when a config key ships undocumented
- **Useful notifications** — each says what happened and what to do, links
  into Minos, and a total DNS outage cannot be muted
- **Extended DNS Errors**, **CNAME-cloak inspection** (default on), **DNS
  rebinding protection**, **access control and rate limiting**, **DDR**,
  **regex rules**, **DNS over QUIC and HTTP/3** (both directions)
- **Password sign-in** — argon2id, signed sessions, optional authenticator
  codes, sign out everywhere; API tokens kept for automation
- **One-click update** — signed releases (Ed25519), a root helper that
  verifies, refuses downgrades and rolls back a release that does not start
- **Devices by name in the Query Log** — rows and the dashboard show device
  names, and the search box finds a device by its label or hostname

## Under consideration

- Replica config sync (see above)

## Considered and declined

- **Newly registered domain lists in the catalog** (October 2026). Hagezi's
  NRD 7-day list is 2.9M names and its high-entropy DGA cut 0.9M. Measured
  on a Raspberry Pi 4 beside StevenBlack: NRD peaks at 401 MB and takes
  19 s per rebuild (every rule change), and its 58 MB download sits just
  under the 64 MB list cap, which a busy week exceeds; DGA settles at
  154 MB and 3.5 s per rebuild. Replayed against a real home network's
  week (1.24M lookups, `go test -tags replay ./internal/lists`), both
  matched three lookups — all of a genuine new website — and nothing
  malicious. Behaviour-based threat detection already covers what DGA
  domains are for. Help explains how to add one as an audit list.
- **Router-assisted device identity** — see the device identity round above.

## Not building (pre-1.0)

Fixed decisions, restated from CLAUDE.md: no DHCP server, no recursive
resolver, no clustering, no plugin system. Tools that want to do
everything end up Technitium — impressive, but not what Minos is for.
Every query gets judged; the judge does not also run the post office.
