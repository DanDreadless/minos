# CLAUDE.md — Minos

> Every query gets judged. Minos is a modern, user-friendly DNS sinkhole
> (Pi-hole alternative) written in Go, light enough for a Raspberry Pi.
> Named for the judge of the underworld: every soul that arrives is judged
> and sentenced — no exceptions, no appeals.

This file is project memory for Claude Code. Read it before making changes.
When architecture or conventions change, update this file in the same PR.

## What Minos is

A DNS forwarding/filtering proxy with an embedded web UI:

- Receives DNS queries on :53 (UDP + TCP)
- Judges each query against compiled blocklists
- Condemned → returns NXDOMAIN or 0.0.0.0 (configurable per policy)
- Passed → forwards to upstream resolvers over DoH/DoT (plaintext optional)
- Streams a live query log to the UI over WebSocket
- Single static binary, frontend embedded via `go:embed`

**We are NOT building (pre-1.0, do not add without a maintainer decision):**
a DHCP server, a recursive resolver, clustering, or plugin systems. If a
task drifts toward these, stop and flag it.

(Per-client group policies were originally on this list; the maintainer
decided to add them in July 2026 — see `internal/clients`. The bounded
design: groups are filter/bypass/block with small per-group domain
overlays, not per-group blocklist subscriptions, which would multiply
matcher memory.)

The feature roadmap and competitive positioning live in `docs/roadmap.md`
— keep them there, not in this file.

## Product vocabulary (the lore layer)

The judgment metaphor is the product identity. Use it consistently in the
UI, docs, and API *labels* — but keep the underlying code and API *fields*
boring and literal so the codebase stays legible to contributors.

| Concept            | UI / docs term      | Code & API term          |
|--------------------|---------------------|--------------------------|
| Blocked query      | Condemned           | `blocked`                |
| Allowed query      | Passed              | `allowed`                |
| Whitelist entry    | Pardon              | `allowlist`              |
| Blacklist entry    | Sentence            | `denylist`               |
| Live query log     | The Docket          | `querylog`               |
| Blocklists         | The Codex           | `lists`                  |
| Pause blocking     | Recess              | `pause`                  |
| Dashboard          | The Tribunal        | `dashboard`              |
| Upstream resolvers | The Ferrymen        | `upstreams`              |

Rules for the theme:
- JSON field names, config keys, log output, and Go identifiers use the
  literal terms. `{"verdict": "blocked"}`, never `{"fate": "condemned"}`.
- The lore lives in UI copy, headings, empty states, and docs flavour
  text. It must never obscure meaning: every themed label gets a plain
  explanation on first use or hover ("Pardons — domains always allowed").
- Restraint is the design principle. One themed word per screen beats
  ten. If a user could be confused about what a button does, the theme
  loses. Error messages are always plain and literal.
- Visual direction: Greek underworld, understated — dark slate/charcoal
  base, a single accent (oxblood red or laurel gold), classical numerals
  or a subtle key/meander motif at most. No cartoon Hades. Think
  courtroom gravitas, not fantasy game.

## Target hardware & performance budgets

Primary targets: Raspberry Pi 4/5 (arm64), amd64 Linux, Docker on both.
Treat these as CI-enforceable budgets, not aspirations:

- RSS under ~150 MB with 2M blocked domains loaded
- p99 blocked-query latency < 1 ms; forwarded adds only upstream RTT
- SD-card safety: query-log writes are batched (see Storage) — never
  write to SQLite on the per-query hot path

## Repo layout

```
cmd/minos/             main.go — flag parsing, wiring, graceful shutdown only
internal/dnsproxy/     DNS server + upstream forwarding (miekg/dns)
internal/clients/      device registry, group policies, ARP/PTR enrichment
internal/filter/       blocklist engine: parsing, compilation, matching
internal/lists/        blocklist fetching, refresh scheduling, format parsers
internal/services/     curated blocked-services catalog (static data, leaf pkg)
internal/importer/     Pi-hole / AdGuard Home migration (append-only merges)
internal/acme/         DNS-01 certificate issuance/renewal for DoT/DoH
internal/notify/       webhook/ntfy event delivery (off the query path)
internal/updates/      opt-in GitHub release check (release builds only)
internal/querylog/     ring buffer + batched SQLite flush
internal/api/          REST + WebSocket handlers (chi router)
internal/config/       config load/validate/persist (YAML)
web/                   frontend source (Svelte + Vite)
web/dist/              built assets, embedded via go:embed (never edit by hand)
deploy/                Dockerfile, compose example, systemd unit
scripts/               maintainer tooling (release.sh — see docs/releasing.md)
docs/                  user-facing docs
```

Rules:
- `internal/filter` must not import `internal/api` or `internal/querylog`.
  Dependency direction is api → (dnsproxy, filter, lists, querylog) → config.
- No package may import from `cmd/`.
- New third-party deps require justification in the PR description. The
  blessed set: `miekg/dns`, `go-chi/chi`, `modernc.org/sqlite`,
  `gopkg.in/yaml.v3`, `github.com/coder/websocket` (the maintained home
  of nhooyr.io/websocket — migrated July 2026), `golang.org/x/crypto`
  (the acme package only — Go-team maintained; chosen over lego's
  ~100-module tree), `golang.org/x/sys` (host metrics on darwin/windows;
  approved August 2026 — it was already an indirect dependency, so making
  it direct added no modules, and the alternative was gopsutil's tree).

## Dev environment & commands

Go 1.22+, Node 20+ (frontend only), golangci-lint, optionally Docker.

```
make dev          # run backend with live reload (air) + vite dev server
make build        # build web/dist then single binary for host arch
make build-arm64  # CGO_ENABLED=0 GOOS=linux GOARCH=arm64 build (Pi target)
make test         # go test ./... -race
make lint         # golangci-lint run
make docker       # multi-arch image build (linux/amd64, linux/arm64)
```

Local testing without binding :53 (needs root): the dev config binds
:5353 — test with `dig @127.0.0.1 -p 5353 doubleclick.net`.

CLI verbs mirror the plain API terms (`minos pause 5m`, `minos status`),
not the lore terms.

## Coding conventions

- Standard Go style; gofumpt formatting; golangci-lint must pass.
- Errors: wrap with `fmt.Errorf("context: %w", err)`. No panics outside
  `main` and truly-impossible states.
- Logging: `log/slog`, structured. The per-query hot path logs at Debug
  only — a Level check guards any allocation-heavy logging.
- Concurrency: blocklist storage is an atomic-pointer swap on reload
  (readers lock-free); querylog uses a buffered channel into a ring-owning
  goroutine, which hands batches to a separate disk-writing goroutine.
  Don't introduce mutexes on the query path.
- Config changes via the API are validated, applied atomically, and
  persisted; the process must never need a restart for a settings change.
- Frontend: Svelte, TypeScript strict, talk to the backend only through
  the documented REST/WS API — no reaching into internals. Themed copy
  lives in a single strings module (`web/src/lib/copy.ts`) so the lore
  layer can be audited, localised, or toned down in one place.

## The filter engine (performance-critical)

- Exact-domain sets compile to a sorted byte slab + packed index keyed on
  the reversed-label form (`com.doubleclick.`): ~31 B/entry (2M domains =
  62 MB heap, 83 MB RSS — inside the budget) with binary-search lookups
  (~535 ns hit at 100k, ~500 ns at 2M — the 1 ms budget doesn't notice).
  The builder still uses maps; Build() compacts, so rebuild peak RSS is
  ~300 MB at 2M domains (transient, off the hot path — fine on Pi 4+).
- AdBlock-syntax rules compile to a small matcher; unsupported AdBlock
  features are skipped with a counted warning, never a parse failure.
- List refresh builds a complete new matcher off the hot path, then
  atomically swaps it in. Never mutate a live matcher.
- Every match result records *which list and rule* matched — "why was
  this condemned" in the UI is a core product feature, not telemetry.
  A one-click pardon flow from any docket entry is sacred UX: it must
  never be more than two clicks from seeing a block to allowing it.

## Storage & SD-card discipline

- Query log: in-memory ring buffer (default 10k entries) is the source
  for the live UI. A background writer flushes batches to SQLite every
  30s or 500 entries, whichever first. WAL mode, `synchronous=NORMAL`.
- An `ephemeral: true` config option disables disk logging entirely.
- Retention enforced by daily pruning job (default 90 days).

## Testing expectations

- Table-driven unit tests for all parsers (hosts, AdBlock, plain lists)
  including hostile input: junk bytes, 10 MB single lines, unicode.
- `internal/dnsproxy` integration tests run a real server on an ephemeral
  port and assert wire-format responses with miekg/dns as the client.
- Benchmarks (`go test -bench`) exist for matcher lookup and list
  compilation; PRs touching `internal/filter` must include before/after
  benchmark output.
- Race detector is always on in CI. A data race is a release blocker.

## Git & PR conventions

- Hosting: a locally-hosted **Gitea** at `vault-tec.local` (migrated from
  GitHub, August 2026), mirrored to GitHub. Use the Gitea web UI or API
  for PRs/issues; `gh` targets the mirror only. **No CI runs on Gitea** —
  it is a NAS container, so Actions are off there and every workflow job
  is gated to `github.server_url`. Verify locally before tagging; the
  mirror's runners do the release build. Anything runner-shaped run ad hoc
  belongs on this laptop (local Docker), never the NAS — and clean up its
  containers afterwards.
- **All work is pushed to Gitea** — every branch, not just `main`. A
  push mirror replicates refs to GitHub automatically (sync-on-commit),
  so never push to GitHub by hand.
- **Releases are published on both platforms** with identical assets:
  `scripts/release.sh vX.Y.Z` verifies, tags, pushes, waits for the
  GitHub build, checksums the artifacts, and attaches them to a Gitea
  release too. Never hand-roll the steps; `--assets-only` is the
  recovery path. Full flow: `docs/releasing.md`.
- Conventional commits: `feat:`, `fix:`, `perf:`, `docs:`, `chore:`.
- Branches: `feat/<slug>`, `fix/<slug>`.
- Keep PRs reviewable: one concern per PR, under ~500 lines diff where
  possible. Update docs/ and this file in the same PR as the change.
- DCO sign-off (`git commit -s`) required. License: GPLv3.

## Security posture

This is security software; hold it to that standard.

- Never disable DNS message validation or size limits "temporarily".
- The API binds to LAN by default with an auth token; anything that
  weakens auth or binds wider needs explicit maintainer sign-off.
- Blocklist fetcher must cap download size, enforce timeouts, and treat
  list content as untrusted input (it is attacker-controllable).
- No telemetry. Ever. This is a product promise.

## Claude Code working agreement

- Prefer small, verifiable steps: make a change, run `make test` and
  `make lint`, then continue. Don't batch large speculative refactors.
- If a task conflicts with the "NOT building" list or the performance
  budgets, pause and ask instead of proceeding.
- When touching the query hot path (dnsproxy receive → filter match →
  respond/forward), run the filter benchmarks and report the delta.
- When writing UI copy, follow the product vocabulary table and its
  restraint rules. Code identifiers and API fields stay literal.
- Never hand-edit `web/dist/` or generated files; regenerate them.
- If you learn something durable about the codebase (a gotcha, a fixed
  decision), add it to this file in the same change.

## Durable learnings (gotchas & fixed decisions)

- **SQLite driver**: we use `modernc.org/sqlite` (pure Go), not
  `mattn/go-sqlite3`. All release builds are `CGO_ENABLED=0` (Pi/arm64,
  Docker scratch image), and the primary dev box has no C toolchain.
  This is the "modernc if we drop cgo" option from the blessed set —
  treat it as decided.
- **Race detector needs cgo**: `go test -race` fails on a machine
  without a C compiler (including this Windows dev box). Run the suite
  without `-race` locally; the race job belongs in Linux CI and remains
  a release blocker.
- **Windows local dev ports**: UDP 5353 falls inside Windows' excluded
  port range — binding it fails with "access permissions". Use
  `127.0.0.1:15353` for local dev on Windows. Also, Windows `nslookup`
  silently ignores `-port`; test with a real DNS client (miekg/dns or a
  Go `net.Resolver` with a custom Dial) instead.
- **Ephemeral-port tests**: `dnsproxy.Server.Start` binds UDP first,
  then binds TCP to the port UDP actually received — required for
  `:0`-based integration tests. Don't "simplify" it back to two
  independent binds.
- **web/dist is committed**: it's embedded via `go:embed all:dist`, so
  a plain `go build` must work without Node. Rebuild it (`make web`)
  whenever `web/src` changes and commit the result.
- **Restart-free settings, and the exceptions**: everything editable via
  `PUT /api/config` applies live (upstreams/mode/TTL via dnsproxy atomic
  pointers; retention/ring size via `querylog.SetRetentionDays`/`Resize`
  wired in main's OnChange). The deliberate exceptions — file-only, need
  a restart — are `dns.listen`, `api.listen`, `dns.tls` (DoT/DoH
  listeners + certificate), and query-log storage (`ephemeral`/
  `db_path`). Don't expose those as editable in the UI.
- **Config load is rollback-safe** (fixed decision): the on-disk load
  path (`parseTolerant`) *ignores* unknown fields (logging a warning), so
  a config written by a newer Minos still loads after a downgrade instead
  of failing on a key the older binary can't model. Strict `KnownFields`
  parsing is kept only for user-uploaded restores (`config.Parse`), where
  an unrecognised key is likely a typo. `save` also copies the prior file
  to `<path>.bak` before every overwrite — a recovery point for a bad
  edit or a post-upgrade rewrite. A config *schema-version* field +
  migration seam is deliberately deferred: adding a new YAML key now would
  itself break rollback to already-frozen strict versions; add it later
  alongside a real migration, once tolerant loading is in the field.
- **Dashboard aggregates read SQLite** (or the ring in ephemeral mode),
  so entries buffered but not yet flushed (≤30s/500) are missing from
  charts. Accepted skew — do not "fix" it by flushing per query.
- **Device policy semantics** (fixed decisions): assignment is by **MAC
  when the device has one** (so a group/block follows it across DHCP
  leases) and by IP otherwise — the only option for a device Minos can't
  see at layer 2 (off-subnet, IPv6, DoT/DoH from a proxy). The hot-path
  table stays **IP-keyed**; a MAC-keyed client is expanded to every live
  IP carrying that MAC in `rebuildPolicies` (off the hot path), so
  `PolicyFor` is unchanged — one map read. A freshly learned MAC↔IP
  association triggers a rebuild from `setMAC` (enrichment worker only, so
  it never races the schedule ticker) to close the brief default-rules
  window on a new lease; the trigger also fires when the association
  *contradicts* config (IP moved away from a configured MAC, or a
  configured client's last-known IP got another device's MAC). A configured
  client always keeps a **valid last-known IP** (`Client.IP`), so an older
  IP-only binary still loads the config after a downgrade (validation
  requires a valid IP) — this is why MAC assignment needed **no new YAML
  key** and sidesteps the deferred schema-version problem. The last-known
  IP is covered as a fallback until ARP re-tags it, but **yields to a
  contradicting live MAC**: a recycled DHCP lease must never inherit the
  previous owner's rules (`ipsForClient`). Client MACs are **unique** in
  validation (canonical `net.ParseMAC` form); the tolerant on-disk load
  self-heals a hand-edited duplicate by demoting later entries to IP-keyed
  rather than failing boot, while strict `Parse` (restores) and API writes
  reject it. The new-device notification is per **physical device**: a
  known MAC (configured, or live on another IP) appearing on a fresh lease
  doesn't re-fire. A client's `blocked: true` overrides its group,
  including bypass. A device-level DNS block is access control, so recess
  does NOT lift it; recess does silence group overlay rules. Group overlay
  pardons beat global denies. Hot-path cost is unchanged: one sync.Map
  touch (~15ns) plus one atomic map read (~5ns), zero allocations — keep
  it that way.
- **Devices merge per physical device** (`Registry.Devices`): a device is
  keyed by MAC when known (else IP), and every IP it has used folds into
  one row — query counts summed, first/last-seen spanning them, the most
  recently active IP the "primary", all of them exposed as `ips[]` for the
  drill-down. This is a **read-path** merge only; the `seen` map and hot
  path stay per-IP. Solves the duplicate rows a power-cycled device left
  when it grabbed a new lease. If ARP never tags an old IP's MAC it can't
  be merged (best-effort) — acceptable.
- **Device identity is best-effort**: MAC comes from the neighbour tables
  (only works when Minos shares the L2 segment), hostname from a
  reverse-DNS lookup. Both run on the enrichment worker, never on the
  query path. Windows (dev-only) reads `arp -a`; Linux reads /proc/net/arp
  for IPv4 **and execs `ip -6 neigh show` for IPv6** (`neigh_linux.go` —
  no /proc equivalent exists and raw netlink isn't worth hand-rolling
  under the no-new-deps rule; a missing `ip` binary logs once at Debug and
  degrades silently). Both families feed one `neighborTable()` map, so an
  IPv6-preferring host merges into its physical-device row and inherits
  MAC-keyed policies with no further code — the old "IPv6 never
  MAC-tagged" limitation is retired. Link-local (fe80::) entries are
  deliberately kept: they carry the merging MAC. `clients.NormalizeMAC`
  canonicalises to lowercase colon form so table-derived and user-entered
  MACs compare equal.
- **Discovery semantics** (fixed decisions, items 4–6 of the identity
  plan): SSDP is the one *active* probe — one M-SEARCH per 5-minute sweep,
  and a LOCATION is fetched **only when it points at the responding
  device's own IP** (anything else is SSRF bait), plain http, no
  redirects, 64 KB cap, 1-fetch/device/hour; `discovery.ssdp` (default on)
  is read per sweep. The DHCP listener (Linux, `discovery.dhcp_listen`,
  default on) is **a listener, never a server** — the "no DHCP server"
  rule stands; it binds :67 only while enabled (minute-cadence reconcile
  releases the port live) and backs off silently if the port is owned. A
  DISCOVER precedes any address, so its identity parks on the MAC (1 h
  TTL) and `setMAC` drains it. Traffic hints (`hints.go`) are the last
  resort: curated first-party OS-check signatures matched at label
  boundaries against the querylog ring (injected via `SetQNameSource` —
  clients must not import querylog), one anonymous device per enrichment
  tick, skipped the moment anything real (name/model/OUI vendor/hint)
  exists, and always rendered as a guess. Every discovered string passes
  `sanitizeDiscoveredName`; nothing ever creates a row for a device that
  hasn't queried Minos.
- **Identity provenance & precedence** (fixed decisions): every discovered
  hostname carries a source — ascending trust `ptr < netbios < mdns <
  ssdp < dhcp` (DHCP is what the device *asked* to be called; PTR is
  second-hand). `setHostname`/`setModel` let equal-or-higher sources
  replace, never lower — so re-running enrichment is idempotent and the
  lookup chain's try-order is about *cost* while the trust order decides
  what *sticks*. The user-set config label always wins at display time and
  lives where it always has. Discovered manufacturer/model
  (mDNS `_device-info`, UPnP — items 3/4 feed `setModel`) are distinct
  from the OUI vendor, and a self-reported manufacturer **overrides** the
  registry vendor in the Device view (it can name a randomized-MAC device
  the registry never could). Merged rows take the best-trusted name across
  the device's IPs, equal trust falling to the most recently active IP.
  API fields: `name_source`, `model` — literal words, no theming.
- **PTR enrichment targets the gateway first** (fixed decision): a bare
  `net.DefaultResolver.LookupAddr` in production goes to the system
  resolver — usually Minos itself — which answers private reverse zones
  with NXDOMAIN (RFC 6303 backstop), so LAN hostnames never resolve. The
  enrichment worker instead tries the default gateway (Linux
  /proc/net/route → `resolverAt(gw:53)`), which knows the DHCP names,
  then falls back to the system resolver, then a **NetBIOS node-status
  query** (`internal/clients/netbios.go` — a unicast NBSTAT to UDP 137 that
  reads the device's own name table, returning the unique 0x00 Workstation
  name; it's the Windows/Samba source mDNS can't see, and uses a *connected*
  socket so a non-NBNS host fast-fails on ICMP unreachable instead of waiting
  the 500ms deadline), then **mDNS direct** (the same
  reverse-PTR query unicast to `ip:5353` on a connected socket — many stacks
  answer direct queries they ignore on multicast, and ICMP-unreachable
  fast-fails the rest), and finally **multicast DNS** (reverse PTR to
  224.0.0.251:5353 with the RFC 6762 unicast-response bit) — the one source
  that works when the router won't answer PTR (the common case). mDNS is sent
  bound to *every* up/multicast/non-loopback interface, concurrently, so a
  multi-homed host (eth+wlan, Docker) still reaches the LAN — a `:0` bind
  silently egresses the wrong adapter. A device that yields a `.local` name
  is also asked for its `_device-info._tcp` TXT `model=` (feeds `setModel`),
  and a **passive announcement listener** (mdns_listen.go, read-only, joins
  the group on every eligible interface) harvests names/models the moment a
  device joins — accepting **self-claims only**: an address record counts
  only when it names the packet's own source IP, only for already-seen
  devices, and only for `.local` names (mDNS must never spoof LAN DNS
  names). NetBIOS/mDNS responses are untrusted input: names are
  bounds-checked and sanitised to printable ASCII before use
  (`sanitizeDiscoveredName` is the shared vet for all discovered strings).
  All off the hot path; Windows (dev-only) skips gateway detection. Separately,
  every device with a known MAC also gets a **vendor label** from
  `internal/oui` — the **full IEEE registry** (MA-L/MA-M/MA-S/IAB, ~58k
  assignments) compiled by gen.go into a ~1.1 MB embedded binary slab
  (sorted prefix arrays + dedup name blob; the old "curated subset for the
  memory budget" claim assumed a naive map and is superseded). Lookups are
  longest-prefix, so MA-S/MA-M carve-outs shadow their parent MA-L block;
  big consumer brands get clean labels via gen.go rules, the long tail keeps
  suffix-trimmed IEEE names. Randomized (locally administered) MACs can
  never match a registry — `oui.IsLocallyAdministered` feeds
  `Device.PrivateMAC` and the UI shows "Private address" instead of a blank
  cell. `.gitattributes` pins `*.bin -text` so autocrlf can't mangle the
  blob. Still roadmapped: DHCP-lease ingestion.
- **Device-page semantics** (fixed decisions): the detail page keys on
  `#/device/<mac-else-ip>` (MAC keeps the URL stable across leases); its
  query history is just `querylog/history?client=<ips[] joined>` — the
  existing exact-IN filter spans the full retention, no new plumbing. The
  stats `hours` caps are 2160 (90 days) with day-sized timeline buckets
  past 168 h. `Client.Notes` (≤4096 chars) is the persistent user field;
  an old binary's tolerant load drops it (and a subsequent old-binary save
  loses it — `.bak` recovers). The Devices list's inline activity panel
  moved to the device page; don't resurrect it.
- **Service pardons semantics** (fixed decisions): a pardoned service
  compiles `services.AllowDomains(name)` as `AddAllow("service:"+name, …)` —
  the deny bundle plus curated `allowExtra` playback/sign-in hosts, so
  `AllowDomains ⊇ Domains` and allowing always shadows blocking the same
  service (allow wins at every label depth; blocked+allowed is legal, not a
  validation error). Extras are **precise hostnames only**, never a shared
  CDN apex (`cloudfront.net`/`akamaihd.net`) — enforced by a services test.
  Allowed verdicts now record `List`/`Rule` in the docket (server.go, after
  the blocked branch) so "why was this passed" names the pardoning list.
  `PUT /api/services` is a **partial update** (`blocked`/`allowed` each
  optional; both absent = 400) — full replace would let pre-`allowed`
  external callers (docs/home-assistant.md recipes) silently clear pardons.
  `blocking.allowed_services` / `groups[].allowed_services` are new YAML
  keys: tolerant on-disk loading keeps downgrades safe (ignored with a
  warning); only pre-tolerant strict binaries refuse, `.bak` recovers.
- **Custom-services semantics** (fixed decisions): user-defined service
  bundles live in `blocking.custom_services` (defs carry their own global
  `blocked`/`allowed` flags) and groups select them via separate
  `custom_services`/`allowed_custom_services` keys. A custom name must
  **never** enter `blocking.services`/`allowed_services` or
  `groups[].services`/`allowed_services` — old binaries validate those
  against the compiled-in catalog and would refuse to boot; keeping customs
  in additive-only keys means a downgrade merely drops them (accepted
  under-blocking, the mirror image of the allowed_services over-blocking
  trade-off). Guarded by `TestCustomServicesStayOutOfCatalogKeys`. Names are
  strict slugs (`validSlug`, max 40) rejected on catalog collision, so the
  shared `service:<name>` pseudo-list namespace stays unambiguous. Deleting
  a def scrubs group references in the same store.Update. The
  encrypted-dns-upstream validation warning deliberately ignores customs.
- **Subscribed-allowlist semantics** (fixed decisions): allowlists live in
  a **separate config slice** (`lists.allow_sources`), never as an
  action/type field on a source — a downgrade then drops the whole unknown
  key and the list merely vanishes (fail-safe over-blocking, same shape as
  `allowed_services`), whereas an ignored per-source field would silently
  turn an allowlist into a blocklist of the very domains it protects. List
  names are unique across both slices; the API still speaks a per-list
  `action: block|allow` (the manager derives it from the slice), and a
  `PUT /api/lists/{name}` action change moves the source between slices.
  Every entry compiles through `AddAllow(src.Name, …)`, so a passing
  verdict names the allowlist in the docket like any pardon. In an
  allow-action **AdBlock** list, membership decides meaning: block-shaped
  rules (`||domain^`, bare domains) and `@@` exceptions all compile as
  allows (`ParseAdblockLine(list, line, allowList)`) — that is how AdGuard
  whitelist filters and Pi-hole v6 "antigravity" lists are written; don't
  "fix" it to skip non-`@@` rules. Imports map sources: Pi-hole v6
  `adlist.type` 1 → allow (v5 DBs have no type column — the importer falls
  back to the typeless query, don't break that); AdGuard
  `whitelist_filters` → allow.
- **Response cache semantics** (fixed decisions): the cache sits *after*
  the filter — verdicts always reflect live rules and blocked answers are
  never cached. Any config change swaps in a fresh cache (that IS the
  flush mechanism; don't add a separate one). Hit/miss counters live on
  the Server so they survive flushes. Cache hit ~128 ns, one sync.Map
  load — no mutexes.
- **Local records beat blocklists** and never leak upstream: unsupported
  qtypes for a local name return an authoritative empty NOERROR rather
  than forwarding. A device-level DNS block still wins over local records.
- **Routes are authoritative, uncached**: conditional-forwarding routes
  have no fallback to default upstreams (a dead router = SERVFAIL, like
  Pi-hole), and routed answers skip the response cache (DHCP-lease churn).
- **This Windows dev box has no `make`**: run the underlying commands
  directly (`go test ./...`, `go vet`, `npm run build`,
  `npx svelte-check`). `gofmt -l` on the working tree is useless here —
  core.autocrlf checks files out with CRLF, so it flags nearly every
  file. Check the **committed blobs** instead (the only thing CI sees):
  `git show HEAD:path.go > tmp.go && gofmt -l tmp.go`, or the whole tree
  via a `git ls-files`/`git show` loop. golangci-lint is NOT a substitute:
  it passed on a file carrying a UTF-8 **BOM** that gofmt rejected, and
  CI caught it. That BOM came from PowerShell 5.1's
  `Set-Content -Encoding utf8`, which always writes one — never rewrite a
  Go file that way; use the Edit tool or a bash heredoc. golangci-lint v2 is at `~/go/bin/golangci-lint`
  (go install, so it always matches the local toolchain); CI runs it
  blocking, installed the same way for the same reason — prebuilt lint
  binaries can lag go.mod's Go version and refuse to run.
- **ACME semantics** (fixed decisions): DNS-01 only (LAN hosts can't do
  HTTP-01); providers are small hand-written clients, never lego. The
  TLS config uses GetCertificate against an atomic pointer, so renewals
  rotate live — dns.tls stays file-only but certs don't need restarts.
  Cached certs with >30 days left are reused (LE rate limits); the
  propagation poll must see the TXT on public resolvers before Accept.
  Credentials live only in the config file, never in the API. E2E runs
  against Pebble in CI (build tag acme_e2e).
- **Encrypted listeners reuse handle()**: DoT is the same dns.Handler on
  a tls.Listen socket; DoH adapts RFC 8484 GET/POST onto a captured
  dns.ResponseWriter — so device policies, local records, Safe Search,
  cache, and the docket apply unchanged over DoT/DoH. Don't fork the
  pipeline for new transports.
- **Dedup + serve-stale semantics** (fixed decisions): the inflight table
  keys like the cache; the leader forwards AND caches, followers copy the
  leader's snapshot (never the live message — ID stamping would race).
  Stale serves advertise TTL 30 (RFC 8767), the window is 6h, and the
  background refresh dedupes through the same table. Safe-search treats
  stale as a miss (no refresh path there). Judging is unaffected — the
  cache still sits after the filter.
- **Private reverse zones are a backstop** (RFC 6303 + RFC 7793 CGNAT):
  matched after local records and only when no route covers the name, so
  auto-PTR and router forwarding always win. Applies to bypass devices
  (resolver correctness, not filtering). Opt out with
  `dns.forward_private_reverse: true`.
- **Bypass-resistance semantics** (fixed decisions): the Firefox DoH canary
  (`use-application-dns.net` → authoritative NXDOMAIN, logged as
  `firefox-doh-canary`) is **default-on**, opt-out via
  `dns.allow_firefox_doh`; it sits before local records in `handle()` and
  deliberately ignores recess (the network still filters — it's just in
  recess). The gate is a length compare, so the common-case hot-path cost is
  ~1 ns — keep it that way. The iCloud Private Relay block is **opt-in**
  (`blocking.block_icloud_private_relay`), compiles the three
  `mask*.icloud.com` names as the `icloud-private-relay` pseudo-list in
  `lists.rebuild()`, and must stay surgical — never block wider icloud.com.
  The `encrypted-dns` service bundle is provider-owned hostnames only. None
  of the three affect Minos's own forwarding (upstream exchanges bypass the
  filter; presets are IP-literal DoH) — the one self-sabotage case, a
  hand-typed hostname DoH/DoT upstream covered by an enabled encrypted-dns
  block, gets a `config.Validate` **warning, never an error** (the OS
  resolver on a production box is usually Minos itself, so the block would
  starve the upstream of its own address).
- **Query-log rows have no unique identity** (fixed decision): a client
  retrying a blocked query inside one millisecond produces rows identical
  in time+qname+client+qtype, so never use entry fields as a Svelte keyed
  {#each} key — duplicate keys throw at runtime and freeze the whole page
  (field bug, v0.16.5). The Docket and device-page tables are deliberately
  unkeyed; keep any future entry list that way (or add a synthetic
  client-side counter id).
- **Themed dropdowns need `appearance: base-select`**: a native `<select>`
  popup is an OS-drawn widget, and Chromium ignores author styles on its
  highlighted row — themed `option` rules alone leave the system blue. app.css
  opts Chromium 135+ into `base-select`, which makes the picker real DOM
  (`::picker(select)`, `::picker-icon`, `option::checkmark`), and keeps the
  plain `option` rules as the Firefox/Safari fallback. `color-scheme: dark`
  on `:root` is the other half: without it every browser-drawn widget (popup,
  date picker, autofill, spinners) renders light regardless.
- **Query-log read indexes** (fixed decisions): the log carries
  `(client, ts, verdict)`, `(list, ts)`, and `(audit_list, ts)` composite
  indexes — without them the device page and Docket list filter walk the
  whole time index hunting for a sparse client/list (measured 1 s per page
  at 2M rows on NVMe; tens of seconds on Pi/SD). Two traps, both measured:
  **SQLite
  ignores a partial index when the query binds the filtered column as a
  parameter** (it can't prove `list = ?` implies `WHERE list != ''` at
  prepare time — the scan silently returns), so these are full indexes; and
  an `(a = ? OR b = ?)` clause can't ride either index, so the list filter
  compiles to two UNION ALL halves each walking its own index. ListNames
  skip-walks distinct names via repeated `MIN(col) > ?` seeks — O(lists),
  never O(rows); plain DISTINCT visits every index entry (146 ms at 2M).
  The client index carries a **trailing `verdict`** so the device summary
  is a *covering* scan: without it the hydration query reads every row of
  the table to answer `SUM(verdict = 'blocked')` — measured 39s against
  1.0s on a 7.4M-row log. `(client, ts)` is a prefix of it, so the narrow
  index it replaced is dropped by the migration (it cost ~12% of the file
  to answer nothing the wider one cannot). Index migration
  (`migrateIndexes`) runs in a **background goroutine** (`buildIndexes`),
  never on the startup path — the one-time build on a large existing DB
  takes minutes on SD, and it used to hold the listeners back for all of
  it. Two consequences: a query naming an index with `INDEXED BY` must
  check `l.indexed.Load()` first (naming a not-yet-built index is a hard
  error, and a worse plan beats a failed request), and any test asserting a
  plan must `<-l.Ready()` after `Open`. Cost: ~+45% file size at 2M rows —
  disk-only, bounded by retention; accepted.
- **Nothing expensive runs before the listeners** (fixed decision,
  September 2026): this was a real field failure — v0.21.0 looked like it
  crashed on restart. `main.serve` called `qlog.ClientsSummary` inline to
  rehydrate the device list, an **unbounded** `GROUP BY client` over the
  whole retained log, before `proxy.Start()` and before the HTTP listener.
  On a 7.4M-row log that was **40 seconds with no DNS on the network, no
  web UI, and nothing in the journal** — indistinguishable from a crash,
  silent, and growing with the log. It hid from `/api/status` too, because
  `s.started` is set in `api.New`, *after* it, so `uptime_seconds` never
  counted the stall. Index migration had the same shape and the same fix.
  The rule now: **`querylog.Open` does only what is instant** (schema,
  `ADD COLUMN`), and everything else — index builds, device hydration —
  runs in a goroutine after the listeners are up. Measured after: 1 ms to
  listening, hydration finishing 1.1s later on a warm log (16.8s on the
  upgrade that also builds the new index, all of it in the background).
  Two things fall out of moving hydration late, both handled and both
  load-bearing: `ClientsSummary` takes a `before` cutoff (process start) so
  history and the live registry's own counting are **disjoint and additive**
  rather than overlapping, and `Registry.Seed` **merges** into a device live
  traffic already created (adding counts, pulling first-seen back, clearing
  `fresh`) instead of skipping it — the old `LoadOrStore`-and-skip silently
  lost all history for any device that queried in the first seconds. Don't
  put anything back in front of the listeners.
- **The ring never waits on the disk** (fixed decision, September 2026): a
  field failure the day v0.21.1 shipped — the Docket looked frozen and the
  server looked dead while DNS was in fact resolving perfectly. `run()` used
  to call `writeBatch` inline, in the same goroutine that owns the ring and
  the WebSocket fan-out, so anything holding SQLite's write lock froze the
  live UI. Moving index migration into the background (v0.21.1) is what made
  that reachable: the build holds the write lock for minutes. **Note the
  fix is not a second connection** — SQLite serialises writers whatever
  connection they are on, so `CREATE INDEX` blocks every other write
  regardless; the answer is to decouple the goroutines, not the handles.
  `run()` is now purely in-memory and hands batches to `flushLoop`, which
  owns *all* writer-side DB calls including the daily prune (the other one
  that could stall the ring). Backpressure: batches accumulate in `run()` up
  to `maxPending` (~1 MB), then the oldest are dropped and counted — the
  same trade `Record` makes on a full channel. Shutdown waits are bounded
  (`shutdownFlushWait`), because an unbounded `Close()` during a migration
  reads as a hang and ends in SIGKILL. `TestRingStaysLiveWhileWriterIsBlocked`
  holds the writer connection and asserts the ring still fills; restore the
  inline flush and it hangs to the test timeout, which is the point.
- **Reads and writes have separate SQLite pools** (fixed decision): `Log.db`
  is the writer (`MaxOpenConns(1)` — inserts, prunes and migrations
  serialise), `Log.rdb` is a 4-connection reader pool. Every read path uses
  `rdb`; only `writeBatch`, `prune` and the migrations use `db`. Before
  this, one connection served everything, so a wide-window `/api/stats`
  froze the whole instance: measured, a 720h stats call left concurrent
  `querylog/history` requests waiting 25.2s, 8.6s and 19.8s, and the 30s
  batch flush queued behind it too. WAL admits concurrent readers alongside
  one writer, so this is what WAL was for. Per-connection PRAGMAs go in the
  **DSN** (`?_pragma=busy_timeout(10000)&...`) — a `db.Exec("PRAGMA …")`
  only touches whichever pooled connection served that call, which is why
  it was safe with one connection and is not with four. `journal_mode` is
  the exception: it is a property of the file, set once by the writer.
- **Stats aggregates are deadline-capped** (fixed decision): Timeline,
  TopBlockedDomains and TopClients group by time, domain and client, and no
  index answers "group 90 days by domain" — each walks every row in the
  window. `/api/stats`, `/api/stats/client` and `/api/stats/lists` cap one
  request at `statsDeadline` (20s) and answer 503 with the window in the
  message. The driver's context cancellation is a real `sqlite3_interrupt`,
  so the work actually stops rather than running on unwatched. The `hours`
  cap stays 1–2160: the windows are legal, they just are not free, and the
  deadline is a better answer than a narrower contract. Counter-intuitive
  but measured: 2160h can be *faster* than 720h, because a range covering
  every row lets SQLite pick a sequential table scan over an index range
  scan with scattered row lookups.
- **Audit-list semantics** (fixed decisions): audit mode is **two matchers,
  never a flag inside one** — `lists.Manager` owns a second audit engine
  (`AuditEngine()`), compiled from only `audit: true` sources, so the
  enforcing matcher stays byte-identical and the audit cost is strictly
  additive. The hot path consults it in the **allowed branch only** (blocked
  queries aren't audited; bypass devices are exempt like all filtering),
  gated by `Engine.Empty()` — no audit lists ≈ one atomic load + branch.
  A pardoned query CAN carry a would-block marker (pardon + audit are both
  shown — that's the feature). Cache hits skip `handle()`'s filter section,
  so audit marks are **sampled at resolution time**, not per hit — accepted,
  documented in api.md; don't "fix" it. `audit` on an allow-source is a
  validation error. The querylog gained `audit_list`/`audit_rule` TEXT
  columns via an idempotent `PRAGMA table_info` + `ALTER TABLE ADD COLUMN`
  migration on open (instant in SQLite, SD-safe) — copy that migration shape
  for any future column.
- **DNSSEC semantics** (fixed decisions): `dns.dnssec` is off | permissive
  | enforce (default off; permissive is the recommended first step).
  Validation runs on the **forwardDedup leader path only** — after the
  upstream answer, before `cache.put` — so followers and cache hits
  inherit the verdict (cache hits never revalidate; the stored AD bit IS
  the verdict) and blocked queries never pay. Chain fetches (DNSKEY/DS)
  ride forwardDedup with `validate=false`: the validator verifies them
  cryptographically itself, and re-validating would recurse. **Enforce
  SERVFAILs Bogus only** — Indeterminate (upstream without DNSSEC
  records, transient fetch failure) deliberately passes, so a bad
  upstream degrades visibility, never resolution; the
  `minos_dnssec_results_total{status="indeterminate"}` counter is the
  signal. A refused bogus answer is docketed as blocked with
  `list: "dnssec"` and the plain failure reason as the rule. Routed
  answers are never validated and get AD cleared when validation is on.
  Bogus is an answer, never a breaker trip. The validator (and its
  validated-key cache) is built once and survives config swaps;
  `SetTrustAnchors` (pre-Start) is the test/lab override. Non-DO clients
  get RRSIG/NSEC/NSEC3 stripped from served answers (we added DO
  upstream ourselves) unless they asked for that very type; AD reaches
  only clients that set DO or AD (RFC 6840 §5.7).
- **DNSSEC visibility** (fixed decisions, August 2026): permissive mode is
  treated as **an audit source, not a special case** — a bogus answer it
  lets through is docketed on `audit_list`/`audit_rule` as `dnssec`, the
  mirror of the `list`/`rule` pair enforce writes. "What changes if I
  enable enforce" is then exactly "these audit marks become blocks", and
  the Docket badge, audit-only filter, list dropdown and `(audit_list, ts)`
  index all work unchanged. Use `dnsproxy.ListDNSSEC`, never the literal.
  `judgeDNSSEC` returns `(audit, err)`; `forwardDedup` carries the reason
  out and stores it on `inflightCall`, so **followers of a deduped
  exchange are attributed like their leader** — same happens-before as
  resp/upstream/err. **Cache hits are not re-validated**, so docket rows
  are a *sample* of `minos_dnssec_results_total{status="bogus"}`, never
  equal to it; documented in api.md and getting-started — don't "fix" it.
  Counters go on `/status` (process-lifetime), the windowed would-block
  figure on `/stats` (from the audit marks) — **never blur the two clocks
  in the UI**. Both are omitted when the mode is off, so the UI hides on
  absence rather than carrying an enabled flag. `/stats` reports whenever
  validation is on, not only in permissive: the mode swaps live and a
  window can span a change. `TopAuditedDomains`/`AuditedTotal` take any
  audit source and **pin `INDEXED BY idx_querylog_audit_ts`** — with the
  aggregate on top SQLite otherwise plans the any-source form on
  `idx_querylog_ts` and walks the whole window (the measured
  seconds-per-query pathology); a test asserts all four shapes keep the
  index. The Tribunal card warns when *indeterminate* exceeds half of all
  judged answers: that means the upstream returns no DNSSEC records and
  validation is near-inert, a state otherwise indistinguishable from a
  healthy quiet card.
- **A live bogus answer is hard to reproduce**: every public resolver
  probed (1.1.1.1, 9.9.9.10, 208.67.222.222, 74.82.42.42) validates and
  SERVFAILs `dnssec-failed.org` before Minos sees it, while 4.2.2.1
  returns the answer but strips DNSSEC records, so everything becomes
  *indeterminate*. Testing the bogus path needs the signed+tampered stub
  in `dnssec_test.go` (`dnssecProxy(t, mode, true, true)`) — don't expect
  to verify it against the real internet.
- **Catalog semantics** (fixed decisions, August 2026): the curated
  catalog in `web/src/lib/blocklists.ts` is verified by **compiling every
  entry through the real parser**, not by probing it —
  `internal/lists/catalog_test.go` under the `catalog` build tag, so
  `go test ./...` never touches the network. A HEAD probe (what it
  replaced) proves only that something answers 200, and misses the three
  failures that matter: content repurposed or emptied, format changed
  (every line silently skipped instead of failing), and size-hint rot.
  The standard is the one the catalog header sets — parses, **zero
  skipped**, rule count within 25% of the hint. Sizes recorded there are
  **compiled rule counts**, never the publisher's marketing figure; OISD
  ships a wildcard-collapsed variant, so the two were never the same
  number and the old "≈500k" for OISD Big was 47% out. Run it after any
  catalog edit: `go test -tags catalog -timeout 20m -v ./internal/lists`
  (the 20m matters — ~25 MB of downloads outruns the default 10m).
  Tiers: balanced → strict → security is a **breakage ladder**; `family`
  is a separate axis (chosen content, not threats) and must not be folded
  into it. Deliberately not carried: annoyances, per-vendor telemetry,
  gambling — a long catalog is harder to choose from than a short one,
  and overlapping lists cost full memory for marginal extra blocking.
- **Hosts preamble is not a skipped rule**: every hosts file opens with
  boilerplate mapping ignorable names to non-sinkhole addresses
  (`255.255.255.255 broadcasthost`, `ff02::1 ip6-allnodes`). The target
  check used to return before `hostsLocalNames` was consulted, so seven
  lines counted as skipped on **every** hosts list — StevenBlack included,
  the fresh-install default. `allLocalNames` now recognises the preamble
  by name whatever it points at, while a genuine mapping
  (`192.168.1.5 nas.lan`) still counts as skipped, because that really is
  a line we were handed and did not use.
- **Host-health semantics** (fixed decisions, August 2026):
  `internal/hostinfo` is a **leaf package** — it imports nothing from
  Minos, so the per-OS files stay testable and the dependency direction
  holds. **Container installs report nothing at all**: `Supported()` is
  false, `Latest()` is nil, the sampler never starts, `/api/host` answers
  exactly `{"supported": false}` (a 200, not a 404 — the endpoint
  answered; the feature does not apply) and no `minos_host_` series are
  exported. The reasoning is that "how is the machine running Minos
  doing?" has two wrong answers inside a container — the host's figures
  describe a machine Minos does not have to itself, the container's
  budget is not the host — and a number confidently about the wrong
  subject is worse than none. The cgroup-aware memory paths stay anyway:
  detection is a heuristic and can miss an unfamiliar runtime, and they
  are what stop Minos reporting the host's 32 GB as its own when it does.
  **Every dynamic reading is optional and absence is never zero** — nil
  in Go, omitted in JSON, no Prometheus series, an em dash in the UI. A
  zero graphs as an idle healthy machine when the truth is nobody
  measured. **Sampling is on a ticker (10s), never in the handler**: CPU
  is a delta, so serving it on demand would sleep inside a request; the
  handler does one atomic load. CPU percent is absent on the first sample
  and whenever the counter stalls or goes backwards. Known platform gaps,
  deliberate: darwin has no CPU percent or memory breakdown (Mach calls
  need cgo, and releases are CGO_ENABLED=0) and windows has no load
  average (not a Windows concept). Disk is the **query log's**
  filesystem, not the largest — on a Pi that is the SD card, and 90% full
  is warned about because a full disk stops Minos recording. `x/sys` is
  used for darwin/windows only; the three Windows counters it lacks
  (`GlobalMemoryStatusEx`, `GetSystemTimes`, `GetTickCount64`) are lazy
  kernel32 procs.
- **CI cross-builds all six release targets** (`cross-build` matrix,
  build + vet): build-tagged per-OS code only fails on its own platform,
  so a wrong tag or a missing symbol is otherwise invisible until release
  day. It caught three x/sys symbols that do not exist.
- **There is a project skill for local verification**:
  `.claude/skills/verify/SKILL.md` — build/launch/drive recipe for this
  Windows box, including the excluded-port range, the async matcher
  rebuild race, and the SQLite flush delay. Read it before hand-rolling a
  local test run. It also records that **no browser-automation harness
  exists here**: UI checking beyond grepping the served `assets/index-*.js`
  for new copy strings is manual.
- **DNSSEC outcome column** (fixed decision, August 2026): the querylog
  carries `dnssec` (secure|insecure|bogus|indeterminate, empty when nothing
  was judged) so the Tribunal's four counters drill into the Docket. It is
  **deliberately unindexed**: every drill-down is time-bounded so it rides
  `(ts)`, and the column is too low-selectivity to earn one — most answers
  are `insecure` (unsigned), which is exactly the case an index would serve
  worst, at the measured ~45% file-growth cost. Same sampling rule as audit
  marks: written on the leader path only, so it records resolutions, not
  lookups, and the counters will never tally with the row count (they are
  also process-lifetime against a retention-bounded log — say so in the UI).
  The API validates the value against the four and 400s otherwise, so a typo
  cannot masquerade as "no matches".
- **Upstream breaker semantics** (fixed decisions): only transport errors
  count (SERVFAIL is an answer); 3 consecutive failures sidestep an
  upstream for 30 s; a lapsed cooldown admits exactly one CAS-elected
  probe query; sick upstreams are still tried as a last resort (a breaker
  must never refuse to try everything); routes are exempt (authoritative,
  no alternative). Health lives on the same name-keyed counters as
  /metrics so it survives config swaps.