# Getting started

## Install

**Install script (Linux amd64/arm64)** — downloads the latest release,
verifies its checksum, installs to `/usr/local/bin`, and sets up the
systemd unit:

```sh
curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh
```

**Docker** — multi-arch images on GHCR (see `deploy/docker-compose.yaml`
for a complete example):

```sh
docker pull ghcr.io/dandreadless/minos:latest
```

**Release archives** — binaries for Linux, macOS, and Windows with a
`checksums.txt` are on the [releases page](https://github.com/DanDreadless/minos/releases).

**From source** (Go 1.22+ and Node 20+ required):

```sh
git clone https://github.com/DanDreadless/minos && cd minos
make build
```

Every path produces the same thing: a single static binary with the web UI
embedded.

## First run

```sh
./bin/minos serve
```

On first start Minos writes a default config file (`minos.yaml`, or the path
given with `-config`) and begins downloading the default blocklist. Binding
port 53 needs root/admin on most systems; on Linux prefer the systemd unit in
`deploy/minos.service`, which grants only `CAP_NET_BIND_SERVICE`.

Then point a client (or your router's DHCP DNS option) at the machine's IP.

## Migrating from Pi-hole or AdGuard Home

One command carries your settings over — run it before starting Minos (or
restart afterwards), since it edits the config file directly:

```sh
# From a Pi-hole install (reads gravity.db and custom.list, read-only):
minos import pihole /etc/pihole

# From AdGuard Home:
minos import adguard /opt/AdGuardHome/AdGuardHome.yaml
```

What comes across: blocklist subscriptions (with their enabled state),
exact allow/deny domains and user rules, local DNS records / rewrites,
and — from AdGuard Home — blocked services that exist in the Minos
catalog. Imports only add: nothing already in your Minos config is
changed, and running an import twice adds nothing new.

What doesn't: regex rules and AdBlock rules with options (Minos matches
whole domains only), allowlist subscriptions, and upstream/DHCP settings.
Every skipped item is printed with a reason so nothing vanishes silently.

## Prepare the host (Raspberry Pi / Linux)

Do these once before first start so port 53 is free and DNS flows to Minos
without contention. Commands assume Raspberry Pi OS / Debian / Ubuntu.

**1. Give the machine a fixed IP.** Every client will point at this address;
it must never change. Either set a DHCP reservation on your router (easiest)
or configure a static address on the Pi.

**2. Free port 53.** Most distros run a local stub resolver that already
occupies it:

```sh
sudo ss -lunp 'sport = :53'      # who owns port 53 right now?
```

If `systemd-resolved` owns it (Ubuntu, some Debian setups):

```sh
sudo mkdir -p /etc/systemd/resolved.conf.d
printf '[Resolve]\nDNSStubListener=no\n' | \
  sudo tee /etc/systemd/resolved.conf.d/minos.conf
sudo systemctl restart systemd-resolved
```

If `dnsmasq` owns it (common when Pi-hole was installed before, or with some
router/AP packages):

```sh
sudo systemctl disable --now dnsmasq
```

**3. Keep the host's own lookups working.** The Pi itself needs a resolver —
during boot Minos isn't up yet, and Minos needs DNS to fetch blocklists and
reach DoH/DoT upstreams. The robust setup is to point the host straight at an
upstream rather than at Minos itself:

```sh
sudo rm -f /etc/resolv.conf     # may be a resolved-managed symlink
printf 'nameserver 1.1.1.1\nnameserver 9.9.9.9\n' | sudo tee /etc/resolv.conf
```

(If you prefer the host to be filtered too, use `nameserver 127.0.0.1` — but
then add a real upstream as the second entry so boot ordering can't wedge
the machine.)

**4. Firewall: LAN only.** Nothing outside your network should reach the
resolver or its admin UI. With ufw, adjusting the subnet to yours:

```sh
sudo ufw allow from 192.168.1.0/24 to any port 53  proto udp
sudo ufw allow from 192.168.1.0/24 to any port 53  proto tcp
sudo ufw allow from 192.168.1.0/24 to any port 8080 proto tcp
sudo ufw enable
```

Never port-forward 53 or 8080 from the internet: an open resolver gets
abused for amplification attacks within hours. Set `api.token` in the config
even on a trusted LAN.

**5. (Busy networks only) raise the UDP buffers.** Defaults are fine for a
home network; if you serve hundreds of clients, let the kernel queue more
datagrams during bursts so none are dropped:

```sh
printf 'net.core.rmem_max=4194304\nnet.core.wmem_max=4194304\n' | \
  sudo tee /etc/sysctl.d/99-minos.conf
sudo sysctl --system
```

Nothing else needs tuning: Minos batches its disk writes (SD-card safe),
blocked answers are served from memory in well under a millisecond, and
forwarded queries add only the upstream round trip.

## Run as a service (auto-start on boot)

A hardened systemd unit ships in `deploy/minos.service`: it runs as a
dynamic non-root user, grants only the capability needed to bind port 53,
and restarts on failure.

```sh
# install the binary and the unit
sudo install -m 755 bin/minos /usr/local/bin/minos
sudo cp deploy/minos.service /etc/systemd/system/

# enable now + on every boot
sudo systemctl daemon-reload
sudo systemctl enable --now minos

# check it
systemctl status minos
journalctl -u minos -f                    # follow the logs
dig @127.0.0.1 doubleclick.net            # should return 0.0.0.0
```

The unit stores state (config, query-log database) in `/var/lib/minos/`.
After an upgrade, replace the binary and `sudo systemctl restart minos`.

**Finally, point your network at it:** set your router's DHCP DNS option to
the Pi's IP so every device picks it up on its next lease renewal — or set
DNS manually on the devices you care about. Keep a second entry *empty*
rather than adding a public fallback: a fallback resolver silently bypasses
all filtering whenever a client feels like using it.

Prefer Docker? `deploy/docker-compose.yaml` has an equivalent setup with
`restart: unless-stopped` for the same auto-start behavior.

## The web interface

Open `http://<host>:8080`. Six pages, one per concern:

- **The Tribunal** (dashboard) — query counters, cache hit rate, a
  24-hour volume chart, the most-blocked domains, the busiest clients,
  and **Recess** controls to pause blocking (5/30 minutes, a custom
  duration, or until resumed).
- **The Docket** (query log) — live-streaming log with search and verdict
  filters. Every blocked entry shows *which list and rule* condemned it and
  has a one-click **Pardon** button; allowed entries can be **Sentenced**
  (blocked) just as fast.
- **Devices** — every client that queries the resolver, identified by IP
  plus MAC address (from the ARP table) and hostname (reverse DNS) where
  available, with query counts and last-seen times. From here you can label
  a device, block its DNS entirely, or assign it to a **group**:
  - `filter` groups get the default rules *plus* the group's own extra
    allow/deny domains and blocked services (a group pardon beats a
    global block),
  - `bypass` groups skip filtering entirely,
  - `block` groups get no DNS service at all.
  Unassigned devices follow the default rules. Filter groups can also
  enforce **Safe Search** for their members. Any group can carry a
  **schedule** — school-night hours, weekend windows — and applies only
  inside it; outside, its devices follow the default rules.

  **Safe Search** (per group here, or network-wide from Settings) answers
  queries for Google, Bing, DuckDuckGo, and YouTube with the provider's
  enforced-safe host (`forcesafesearch.google.com`, `strict.bing.com`,
  `safe.duckduckgo.com`, `restrictmoderate.youtube.com`), so filtered
  results are enforced by the provider itself and can't be switched off
  in the page settings. Only exact search hostnames are rewritten —
  Gmail, Maps, and other subdomains are untouched.
- **The Codex** (blocklists) — add, enable/disable, or remove list
  subscriptions and refresh them on demand, with per-list rule counts and
  fetch errors. The **Blocked services** card blocks a whole service
  (TikTok, YouTube, Discord…) with one checkbox — for every device, or
  per group from the Devices page.
- **Pardons & Sentences** (allow/deny domains) — manage both lists, plus a
  "judge a domain" tool that shows exactly which rule decides any name's
  fate before you ever query it. The **Local DNS** card lives here too:
  A/AAAA/CNAME records (wildcards like `*.home.lab` included) that Minos
  answers itself — they beat the blocklists, never leave your network, and
  address records answer reverse (PTR) lookups automatically.
- **Settings** — everything below is editable here and applies immediately,
  no restart: upstream resolvers and their order, conditional forwarding
  (send `lan` or your reverse zone to the router so DHCP hostnames keep
  resolving), the response cache (repeat queries answered from memory —
  the dashboard shows the hit rate), blocking mode and TTL, network-wide
  Safe Search, list refresh interval, query-log retention and buffer
  size, the API token, a one-click YAML config backup, and an opt-in
  daily update check (a "vX.Y.Z available" link appears in the sidebar
  when a newer release exists — nothing is sent beyond the request).

If you set `api.token` (in the config file or from Settings), the UI and
CLI require it.

## Encrypted DNS for your devices (DoT / DoH)

Minos can serve DNS-over-TLS and DNS-over-HTTPS itself, so phones and
laptops resolve through it encrypted — and Android's Private DNS keeps
your filtering active even when apps would otherwise hardcode a public
DoH resolver.

```yaml
dns:
  tls:
    cert_file: /var/lib/minos/fullchain.pem
    key_file: /var/lib/minos/privkey.pem
    dot_listen: ":853"     # DNS-over-TLS (Android Private DNS)
    doh_listen: ":8443"    # DNS-over-HTTPS at /dns-query
```

Both listeners are optional and share one certificate. Like the plain
listen addresses, these settings are file-only — restart to change them.
Everything applies unchanged over the encrypted listeners: device
policies, local records, Safe Search, the cache, and the query log.

**The certificate must match a real hostname** that clients validate —
Android Private DNS takes a hostname, not an IP. The usual home setup:
a DNS-01 Let's Encrypt certificate for a name like `dns.example.com`
(issued without exposing anything to the internet), plus a local record
in Minos pointing that name at the server's LAN IP. A self-signed CA
works too if you install it on the devices.

Point Android at it: Settings → Network → Private DNS → the hostname on
the certificate. iOS/macOS take DoH/DoT via a configuration profile.

Keep the firewall LAN-only for these ports just like port 53:

```sh
sudo ufw allow from 192.168.1.0/24 to any port 853  proto tcp
sudo ufw allow from 192.168.1.0/24 to any port 8443 proto tcp
```

## Monitoring with Prometheus / Grafana

`GET /metrics` on the API port serves Prometheus exposition format:
query/block counters, cache hit rate, per-upstream request counts,
failures, and cumulative latency, per-list rule counts, and pause state.
It is scrape-only — Minos never pushes data anywhere, so the no-telemetry
promise holds; these are your own numbers on your own network.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: minos
    static_configs:
      - targets: ["192.168.1.2:8080"]
    authorization:          # only if you set api.token
      credentials: your-api-token
```

Mean upstream latency in PromQL:
`rate(minos_upstream_duration_seconds_total[5m]) / rate(minos_upstream_requests_total[5m])`

## Configuration

Everything lives in one YAML file and every setting can be changed through
the API or Settings page without a restart (the listen addresses —
including `dns.tls` — and the query-log storage location are the
exceptions; those are file-only). Key sections:

```yaml
dns:
  listen: ":53"
  upstreams:                # tried in order — "the ferrymen"
    - address: https://cloudflare-dns.com/dns-query
      protocol: doh         # udp | tcp | dot | doh
  cache:                    # answer repeat queries from memory
    enabled: true
    max_entries: 10000      # ~500 B per entry
    min_ttl: 10             # seconds; keep short-lived answers at least this long
    max_ttl: 3600           # never serve a cached answer longer than this
    serve_stale: true       # RFC 8767: answer from an expired entry (up to
                            # 6h old) and refresh in the background
  local_ttl: 300            # TTL on locally answered records
  local_records:            # names answered here, never sent upstream
    - name: nas.home.lab
      a: [192.168.1.10]     # also answers the reverse (PTR) lookup
    - name: "*.home.lab"    # wildcard: any subdomain (not home.lab itself)
      a: [192.168.1.10]
    - name: media.home.lab
      cname: nas.home.lab   # alias; cname and a/aaaa are mutually exclusive
  routes:                   # conditional forwarding (split DNS)
    - domains: [lan, home.arpa, 1.168.192.in-addr.arpa]
      upstream:             # e.g. your router, which knows DHCP hostnames
        address: 192.168.1.1:53
        protocol: udp
  # Private reverse zones (192.168.x.x PTR etc.) answer NXDOMAIN locally
  # per RFC 6303 instead of leaking to upstreams. Local records and routes
  # take precedence; set true only if your upstreams know these zones.
  forward_private_reverse: false
  tls:                      # serve DoT/DoH to clients (file-only: restart to change)
    cert_file: /var/lib/minos/fullchain.pem
    key_file: /var/lib/minos/privkey.pem
    dot_listen: ":853"      # DNS-over-TLS (Android Private DNS); empty = off
    doh_listen: ":8443"     # DNS-over-HTTPS at /dns-query; empty = off
blocking:
  mode: zero_ip             # or nxdomain
  allowlist: []             # pardons: always allowed
  denylist: []              # sentences: always blocked
  services: [onlyfans]      # curated service bundles, blocked for everyone
  safe_search: false        # rewrite search engines to enforced-safe variants
groups:                     # device policies (all optional)
  - name: kids
    mode: filter            # filter | bypass | block
    denylist: [tiktok.com]  # extra blocks for members only
    services: [snapchat]    # service bundles for members only
    safe_search: true       # enforce safe search for members only
    schedule:               # optional: group active only in this window
      days: [sun, mon, tue, wed, thu]   # empty/omitted = every day
      start: "21:00"        # server-local time
      end: "07:00"          # before start = runs past midnight
  - name: trusted
    mode: bypass            # members skip filtering entirely
clients:                    # device assignments, keyed by IP
  - ip: 192.168.1.50
    name: "Danny's laptop"  # free-text label
    group: trusted
  - ip: 192.168.1.60
    blocked: true           # refuse all DNS from this device
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
update_check: false         # opt-in: ask GitHub for the latest release once
                            # a day; nothing is sent beyond the request itself
```

## CLI

```sh
minos status     # show counters and pause state
minos pause 30m  # pause blocking (blank duration = until resumed)
minos resume     # resume blocking
minos import pihole /etc/pihole            # migrate Pi-hole settings
minos import adguard AdGuardHome.yaml      # migrate AdGuard Home settings
minos version
```

`status`, `pause`, and `resume` talk to the running instance over its HTTP
API, honoring `-config` to find the address and token. `import` edits the
config file directly — run it while Minos is stopped, or restart afterwards.
