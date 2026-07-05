<p align="center">
  <img src="docs/assets/minos_logo_and_banner.png" alt="Minos — DNS server" width="420" />
</p>

[![X (formerly Twitter) Follow](https://img.shields.io/twitter/follow/DanDreadless?link=https%3A%2F%2Fx.com%2FDanDreadless)](https://x.com/DanDreadless)

# Minos

> Every query gets judged.

Minos is a modern, user-friendly DNS sinkhole (a Pi-hole alternative) written
in Go — a single static binary with an embedded web UI, light enough for a
Raspberry Pi. Named for the judge of the underworld: every DNS query that
arrives is judged against your blocklists and sentenced — no exceptions,
no appeals (well, except pardons).

## What it does

**Filtering**

- Listens for DNS queries on `:53` (UDP + TCP) and judges each one against
  compiled blocklists (hosts, plain, and AdBlock formats)
- Blocked queries get `0.0.0.0`/`::` or `NXDOMAIN` (your choice); allowed
  queries are forwarded upstream over DoH or DoT (plaintext optional)
- Every blocked query shows *which list and rule* condemned it, with a
  one-click pardon from the live log — never more than two clicks from
  seeing a block to allowing it
- Repeat queries answered from a built-in response cache — no upstream trip;
  concurrent identical queries collapse into one, and stale answers are
  served instantly while a fresh one is fetched behind them (RFC 8767)
- A failing upstream is sidestepped automatically and re-probed until it
  recovers — one dead resolver never slows the whole network

**For the household**

- Device tracking (IP, MAC, hostname) with per-device groups: extra rules,
  full bypass, or no DNS at all — and a one-click block for any device
- One-click blocked services (TikTok, YouTube, Discord…) — globally or
  per group, with optional schedules ("no social media after 21:00")
- Safe Search enforcement (Google, Bing, DuckDuckGo, YouTube) — network-wide
  or per group, enforced by the provider so it can't be switched off

**For the network**

- Local DNS records for your LAN (`nas.home.lab`, wildcards, CNAMEs, PTR)
- Conditional forwarding: route `lan` or your reverse zone to the router
- Private reverse zones answered locally (RFC 6303) — `192.168.x.x`
  lookups never leak to public resolvers
- Serves encrypted DNS to clients: DoT for Android Private DNS, DoH at
  `/dns-query` — bring a certificate, filtering follows your devices

**Running it**

- Web dashboard with query charts, top blocked domains, busiest clients,
  cache hit rate, and a live query log streamed over WebSocket
- Full management from the UI: blocklists, allow/deny domains, upstreams,
  blocking mode, retention, API token — all applied live, no restarts
- One-command migration: `minos import pihole /etc/pihole` or
  `minos import adguard AdGuardHome.yaml`
- Prometheus `/metrics` with a ready-made [Grafana dashboard](deploy/grafana-dashboard.json)
  — scrape-only, never pushes
- A fully [documented REST API](docs/api.md) with
  [Home Assistant recipes](docs/home-assistant.md) — everything the UI
  does, your automations can do
- Webhook / [ntfy](https://ntfy.sh) notifications: a new device on your
  network, an upstream failing or recovering, a new release
- Opt-in update check — a "vX.Y.Z available" link in the sidebar; off by
  default, and nothing is sent beyond the request itself
- Batched SQLite persistence that respects SD cards
- No telemetry. Ever.

## Quick start

On a Raspberry Pi or any Linux box — amd64, arm64, or 32-bit ARM (Pi
Zero through Pi 4 on 32-bit Pi OS) — one line installs the latest
release and the systemd unit:

```sh
curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh
sudo systemctl enable --now minos
```

Prefer Docker? Multi-arch images are on GHCR:

```sh
docker run -d --name minos -p 53:53/udp -p 53:53/tcp -p 8080:8080 \
  -v minos-data:/data ghcr.io/dandreadless/minos:latest
```

Or build from source (Go 1.22+, Node 20+):

```sh
make build          # builds web UI + single binary into bin/minos
./bin/minos serve   # starts DNS on :53 and the web UI on :8080
```

First run writes a commented default config to `minos.yaml`. Point a device's
DNS at the machine running Minos and open `http://<host>:8080`.

CLI verbs talk to the running instance:

```sh
minos status        # counters, rules, pause state
minos pause 5m      # pause blocking for five minutes
minos resume        # resume blocking
minos import pihole /etc/pihole   # bring your Pi-hole settings with you
```

For local development without root, set `dns.listen: ":5353"` in the config
and test with `dig @127.0.0.1 -p 5353 doubleclick.net`.

## Deploying on a Raspberry Pi (or any Linux box)

The install script above puts the binary in `/usr/local/bin` and installs
the hardened systemd unit (built from source instead? `sudo install -m 755
bin/minos /usr/local/bin/minos && sudo cp deploy/minos.service
/etc/systemd/system/`).

Before first start, free port 53 (disable the `systemd-resolved` stub
listener or `dnsmasq`), give the machine a fixed IP, and firewall ports
53/8080 to your LAN only — the step-by-step walkthrough is in
[docs/getting-started.md](docs/getting-started.md), including host tuning
notes for busy networks. Then point your router's DHCP DNS option at the
machine and every device follows.

`deploy/` also has a multi-arch Dockerfile and compose example
(`restart: unless-stopped` gives the same boot behavior).

## Documentation

- [Getting started](docs/getting-started.md) — install, host prep,
  systemd, encrypted DNS, monitoring, and the full config reference
- [REST API reference](docs/api.md) — every endpoint, with examples
- [Home Assistant recipes](docs/home-assistant.md) — blocking switch,
  sensors, bedtime automations, events on your phone
- [Roadmap](docs/roadmap.md) — what shipped, what's under consideration

## Roadmap

Everything from the July 2026 competitive review has shipped — the
resolver core (cache, dedup, serve-stale, failover health, private
reverse zones), family controls, the Pi-hole/AdGuard importer,
client-facing DoT/DoH, metrics, notifications, and the release
pipeline. What's under consideration next (ACME automation, UI import,
DNSSEC validation) lives in [docs/roadmap.md](docs/roadmap.md).

## Developing

Go 1.22+, Node 20+. `make test` runs the Go suite with the race detector;
`make lint` runs golangci-lint and the frontend type check; `make bench`
runs the filter engine benchmarks. See `CLAUDE.md` for architecture,
conventions, and performance budgets.

## License

GPLv3.
