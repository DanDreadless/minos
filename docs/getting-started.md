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

## The Tribunal (web dashboard)

Open `http://<host>:8080`. You'll see:

- **Judged / Condemned** — total queries handled and how many were blocked.
- **The Docket** — the live query log. Every blocked entry shows *which list
  and rule* condemned it, and a **Pardon** button that allowlists the domain
  in one click.
- **Recess** — pause blocking for 5 or 30 minutes, or until you resume.

If you set `api.token` in the config, the UI and CLI require it.

## Configuration

Everything lives in one YAML file and every setting can be changed through
the API without a restart (the DNS listen address is the one exception).
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
