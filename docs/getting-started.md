# Getting started

## Install

Build from source (Go 1.22+ and Node 20+ required):

```sh
git clone <repo-url> minos && cd minos
make build
```

The result is a single static binary at `bin/minos` with the web UI embedded.

## First run

```sh
./bin/minos serve
```

On first start Minos writes a default config file (`minos.yaml`, or the path
given with `-config`) and begins downloading the default blocklist. Binding
port 53 needs root/admin on most systems; on Linux prefer the systemd unit in
`deploy/minos.service`, which grants only `CAP_NET_BIND_SERVICE`.

Then point a client (or your router's DHCP DNS option) at the machine's IP.

## The web interface

Open `http://<host>:8080`. Five pages, one per concern:

- **The Tribunal** (dashboard) — query counters, a 24-hour volume chart,
  the most-blocked domains, the busiest clients, and **Recess** controls
  to pause blocking (5/30 minutes, a custom duration, or until resumed).
- **The Docket** (query log) — live-streaming log with search and verdict
  filters. Every blocked entry shows *which list and rule* condemned it and
  has a one-click **Pardon** button; allowed entries can be **Sentenced**
  (blocked) just as fast.
- **The Codex** (blocklists) — add, enable/disable, or remove list
  subscriptions and refresh them on demand, with per-list rule counts and
  fetch errors.
- **Pardons & Sentences** (allow/deny domains) — manage both lists, plus a
  "judge a domain" tool that shows exactly which rule decides any name's
  fate before you ever query it.
- **Settings** — everything below is editable here and applies immediately,
  no restart: upstream resolvers and their order, blocking mode and TTL,
  list refresh interval, query-log retention and buffer size, the API
  token, and a one-click YAML config backup.

If you set `api.token` (in the config file or from Settings), the UI and
CLI require it.

## Configuration

Everything lives in one YAML file and every setting can be changed through
the API or Settings page without a restart (the two listen addresses and
the query-log storage location are the exceptions — those are file-only).
Key sections:

```yaml
dns:
  listen: ":53"
  upstreams:                # tried in order — "the ferrymen"
    - address: https://cloudflare-dns.com/dns-query
      protocol: doh         # udp | tcp | dot | doh
blocking:
  mode: zero_ip             # or nxdomain
  allowlist: []             # pardons: always allowed
  denylist: []              # sentences: always blocked
lists:
  sources:
    - name: StevenBlack
      url: https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
      format: hosts         # hosts | plain | adblock
      enabled: true
  refresh_interval: 24h
querylog:
  ephemeral: false          # true = never touch disk
  db_path: minos.db
  ring_size: 10000
  retention_days: 90
api:
  listen: 0.0.0.0:8080
  token: ""                 # set one if the LAN isn't fully trusted
```

## CLI

```sh
minos status     # show counters and pause state
minos pause 30m  # pause blocking (blank duration = until resumed)
minos resume     # resume blocking
minos version
```

All CLI verbs use the HTTP API of the running instance, honoring `-config`
to find the address and token.
