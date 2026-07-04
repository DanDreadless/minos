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
internal/querylog/     ring buffer + batched SQLite flush
internal/api/          REST + WebSocket handlers (chi router)
internal/config/       config load/validate/persist (YAML)
web/                   frontend source (Svelte + Vite)
web/dist/              built assets, embedded via go:embed (never edit by hand)
deploy/                Dockerfile, compose example, systemd unit
docs/                  user-facing docs
```

Rules:
- `internal/filter` must not import `internal/api` or `internal/querylog`.
  Dependency direction is api → (dnsproxy, filter, lists, querylog) → config.
- No package may import from `cmd/`.
- New third-party deps require justification in the PR description. The
  blessed set: `miekg/dns`, `go-chi/chi`, `modernc.org/sqlite`,
  `gopkg.in/yaml.v3`, `github.com/coder/websocket` (the maintained home
  of nhooyr.io/websocket — migrated July 2026).

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
  (readers lock-free); querylog uses a buffered channel into a single
  writer goroutine. Don't introduce mutexes on the query path.
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
- **Dashboard aggregates read SQLite** (or the ring in ephemeral mode),
  so entries buffered but not yet flushed (≤30s/500) are missing from
  charts. Accepted skew — do not "fix" it by flushing per query.
- **Device policy semantics** (fixed decisions): assignment is by source
  IP (MAC is informational; use DHCP reservations for stability). A
  client's `blocked: true` overrides its group, including bypass. A
  device-level DNS block is access control, so recess does NOT lift it;
  recess does silence group overlay rules. Group overlay pardons beat
  global denies. Hot-path cost is one sync.Map touch (~15ns) plus one
  atomic map read (~5ns), zero allocations — keep it that way.
- **Device identity is best-effort**: MAC comes from the ARP/neighbor
  table (only works when Minos shares the L2 segment; IPv4 only for
  now), hostname from a reverse-DNS lookup. Both run on the enrichment
  worker, never on the query path. Windows reads `arp -a`; Linux reads
  /proc/net/arp.
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
  directly (`go test ./...`, `gofmt -l`, `go vet`, `npm run build`,
  `npx svelte-check`). golangci-lint v2 is at `~/go/bin/golangci-lint`
  (go install, so it always matches the local toolchain); CI runs it
  blocking, installed the same way for the same reason — prebuilt lint
  binaries can lag go.mod's Go version and refuse to run.
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
- **Upstream breaker semantics** (fixed decisions): only transport errors
  count (SERVFAIL is an answer); 3 consecutive failures sidestep an
  upstream for 30 s; a lapsed cooldown admits exactly one CAS-elected
  probe query; sick upstreams are still tried as a last resort (a breaker
  must never refuse to try everything); routes are exempt (authoritative,
  no alternative). Health lives on the same name-keyed counters as
  /metrics so it survives config swaps.