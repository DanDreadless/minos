# Getting started

## Install

**Install script (Linux amd64/arm64/armv7/armv6)** — downloads the latest release,
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
The dashboard shows a short setup checklist until the first queries arrive
— it walks through exactly these steps and links the relevant pages.

## CLI

```sh
minos status     # show counters and pause state
minos pause 30m  # pause blocking (blank duration = until resumed)
minos resume     # resume blocking
minos import pihole /etc/pihole            # migrate Pi-hole settings
minos import adguard AdGuardHome.yaml      # migrate AdGuard Home settings
minos logs -f -level warn                  # follow the application log
minos version
```

`status`, `pause`, and `resume` talk to the running instance over its HTTP
API, honoring `-config` to find the address and token. `import` edits the
config file directly — run it while Minos is stopped, or restart afterwards.

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

## Upgrading

Your settings are never in the binary — configuration and query history live
in `/var/lib/minos/` (systemd `StateDirectory`) or a mounted Docker volume,
which upgrades leave untouched. So every upgrade below is just "replace the
program, restart"; blocklists, allow/deny rules, devices, groups, local
records, and history all carry over. Minos also keeps a `minos.yaml.bak`
recovery point beside the config.

Upgrade the way you installed:

**From the web interface (quick-install installs):** with the update check
on (Settings → System) and a password set, the "new version" banner has an
**Update now** button. Minos downloads the release, checks its signature,
installs it and restarts — and puts the previous version back by itself if
the new one does not start. The install script adds the updater the first
time you run it with v0.31.0 or later, so run the command below once and
the button appears from then on. Docker installs keep using the pull
command (a container cannot replace its own image).

**Quick-install script (binary + systemd — the Raspberry Pi default):**
re-run the installer. It fetches the latest release, verifies its checksum,
and replaces `/usr/local/bin/minos`. Your state is left alone; the systemd
unit is refreshed only if this release changed it, and the old one is kept
as `minos.service.bak`.

It does *not* restart the service by default — a DNS resolver going away is
a whole-network event, and the script won't decide that for you. It says so
plainly at the end, and then you restart:

```sh
curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh
sudo systemctl restart minos
minos version                             # confirm the new version
```

Or have it restart for you:

```sh
curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo MINOS_RESTART=1 sh
```

Replacing a running binary is safe — the live process keeps the old file
open until the restart loads the new one.

Minos serves DNS and the web UI within milliseconds of starting, whatever
the size of your query log. An upgrade that introduces a new query-log index
builds it in the background afterwards, which can take minutes on a large
log on an SD card; everything stays available while it does, and
`journalctl -u minos` announces the start and finish.

**Docker:** pull the new image and recreate the container; the config volume
is preserved. Note that `deploy/docker-compose.yaml` uses `network_mode:
host` — under a bridge network every query arrives from the bridge gateway,
so Minos sees one client instead of your network and the Devices page,
groups, per-device rules and discovery all stop working. The compose file
carries a commented bridge block for non-Linux hosts, where that trade is
the only option:

```sh
docker compose pull && docker compose up -d
```

**Built from source:** fetch the release tag and rebuild, then restart
however you run it:

```sh
git fetch --tags && git checkout v0.7.0 && make build
```

After restarting, the sidebar footer shows the running version and the
"update available" notice clears.

The in-app notice shows the command for how it thinks this instance was
installed (a build-time stamp, refined by runtime container detection). If
an unusual deployment — a distro package, a k8s manifest — guesses wrong,
pin it in the config:

```yaml
update_install_method: docker   # binary | docker | source
```

## Encrypted DNS for your devices (DoT / DoH)

Minos can serve DNS-over-TLS and DNS-over-HTTPS itself, so phones and
laptops resolve through it encrypted — and Android's Private DNS keeps
your filtering active even when apps would otherwise hardcode a public
DoH resolver.

With either listener on, Minos also answers **DDR** (Discovery of
Designated Resolvers, RFC 9462): a device using Minos over plain DNS asks
`_dns.resolver.arpa` and learns the DoT and DoH endpoints, and Windows 11,
iOS and macOS switch to encrypted DNS by themselves. They only do so when
the certificate names the address they were using — a certificate that
covers Minos's LAN IP (an IP SAN), not just a hostname — otherwise they
stay on plain DNS, which keeps working as before. Minos never forwards
`resolver.arpa`, so an upstream resolver cannot hand your devices its own
encrypted endpoint and move them off Minos.

**DNS over QUIC and HTTP/3.** Add `doq_listen: ":853"` to serve DNS over
QUIC (RFC 9250 — UDP, so it shares the number with DoT's TCP port; clients
such as AdGuard and the Android/iOS apps that support it get TLS 1.3
encryption without TCP's head-of-line stalls, and keep working as a phone
moves between Wi-Fi and mobile data). The DoH listener also serves HTTP/3
on the same port over UDP and advertises it, so browsers and clients that
speak it upgrade by themselves; if that UDP port is taken, DoH carries on
over HTTP/2 and the log says why. DDR advertises DoQ and HTTP/3 too.
Upstreams can use them as well: protocol `doq` with `host:port`, or `doh3`
with an `https://` URL (the Cloudflare HTTP/3 and AdGuard DNS-over-QUIC
presets are in the picker).

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
Android Private DNS takes a hostname, not an IP. A self-signed CA works
if you install it on every device, but the good path is a real
certificate — and Minos can fetch and renew one itself.

### Automatic certificates (ACME)

Replace `cert_file`/`key_file` with an `acme` block and Minos obtains a
Let's Encrypt certificate via the DNS-01 challenge (the only ACME
challenge a LAN-only host can complete), renews it 30 days before
expiry, and rotates it into the live listeners without a restart:

```yaml
dns:
  tls:
    dot_listen: ":853"
    doh_listen: ":8443"
    acme:
      email: you@example.com
      domain: dns.example.com
      provider: cloudflare      # cloudflare | desec | duckdns | rfc2136
      api_token: "..."          # a scoped token (Zone read + DNS edit)
```

Provider notes:

- **cloudflare** — create an API token scoped to the zone with
  `Zone.Zone: Read` and `Zone.DNS: Edit`.
- **desec** — a [deSEC.io](https://desec.io) token; free and built for
  exactly this.
- **duckdns** — `domain` must be your `something.duckdns.org` name;
  `api_token` is your DuckDNS token.
- **rfc2136** — dynamic updates to your own DNS server:

  ```yaml
      provider: rfc2136
      server: 192.168.1.5:53
      tsig_name: minos-key
      tsig_secret: "base64=="
      tsig_algorithm: hmac-sha256   # default
  ```

Details worth knowing: the account key and issued certificate live in
`acme/` next to the config file (override with `cache_dir`); a cached
certificate with more than 30 days left is reused on restart (Let's
Encrypt rate limits are real); while testing, point `directory_url` at
the staging CA (`https://acme-staging-v02.api.letsencrypt.org/directory`)
so failed experiments don't burn production quota; and if issuance
fails, Minos retries hourly and sends one notification per failure
streak. Don't forget the **local record** mapping `dns.example.com` to
the server's LAN IP so clients can actually reach it.

Point Android at it: Settings → Network → Private DNS → the hostname on
the certificate. iOS/macOS take DoH/DoT via a configuration profile.

Keep the firewall LAN-only for these ports just like port 53:

```sh
sudo ufw allow from 192.168.1.0/24 to any port 853  proto tcp
sudo ufw allow from 192.168.1.0/24 to any port 8443 proto tcp
```

## The web interface

Open `http://<host>:8080`. One page per concern — and **Help** in the menu
(or the **?** beside any page title) explains each one and every setting;
the same guide is [docs/user-guide.md](user-guide.md).

The bottom of the side menu shows whether Minos is blocking right now
("Blocking · 73k rules", "Blocking paused until 14:20", or — in red — "Not
blocking — no blocklist rules loaded"), with **Pause blocking** (5 or 30
minutes, an hour, until you resume, or any duration) and **Check a
domain**, which also opens with Ctrl+K / ⌘K on any page: type or paste a
name or URL to see whether it is blocked and by which rule, then allow or
block it, or open it in the Query Log. On a phone, a paused or
not-blocking state also shows in the top bar.

- **Dashboard** — what your network did over the window you pick (24
  hours, 7, 30 or 90 days): queries, blocked, block rate, a volume chart,
  the most-blocked domains and the busiest devices — every number for the
  same window, and each one a link into the Query Log for exactly those
  queries. A line underneath gives the totals since Minos last started.
  **Needs attention** appears at the top only when something does — no
  rules loaded, a list retired or failing, a resolver not answering (or all
  of them), no API token, the disk nearly full, DNSSEC unable to check
  answers, or new warnings in the Logs — each with a link to the fix.
- Answers also carry the reason for tools that can read it (RFC 8914
  Extended DNS Errors, sent only to clients that use EDNS0): `dig` shows
  `EDE: 15 (Blocked): (Blocked by Minos: StevenBlack)` on a blocked name,
  Prohibited for a device whose DNS is blocked, DNSSEC Bogus with the
  reason, Stale Answer when served from cache during an outage, and
  Network Error when no upstream answered.
- **Query Log** — the newest queries, streaming live, and a
  search across the whole retained history. Every blocked entry shows
  *which list and rule* blocked it and has a one-click **Allow** button;
  allowed entries can be **Blocked** just as fast, and **Check**
  says how a domain would be judged now, with the current lists.
  - With no filter the page is **Live**: rows arrive as they happen, and
    **Pause** holds them still while you read (new ones wait, counted, until
    you resume). Any filter switches to **Results**: matches from the full
    history, newest first, with **Load older** and new matches joining at
    the top.
  - Filter with the result buttons (all / blocked / allowed / would
    block), the list dropdown, and a time range (last hour to 90 days, or
    all history). **Click any device, domain, type, list or resolver in a
    row** to filter by it. Each filter shows as a chip in the search box;
    × or Backspace removes it.
  - The search box takes a small syntax — `blocked`, `allowed`,
    `would-block`, `client:192.168.1.5`, `domain:example.com` (that exact
    name, not its subdomains), `type:AAAA`, `list:StevenBlack` (quote names
    with spaces: `list:"My list"`), `upstream:1.1.1.1:853`, `dnssec:bogus`,
    and `-cdn` to leave out names containing "cdn". Anything else matches
    domains and device addresses containing it. Press `/` to jump to the
    search, Enter to apply, Esc to clear.
  - **Export CSV / JSON** downloads every match of the current filter (up
    to 100,000 rows) as one file.
  - The address bar keeps the filter, so a search can be bookmarked or
    shared. Drill-downs from the Dashboard and device pages open on the same
    exact name and time window as the count you clicked.
- **Devices** — every client that queries the resolver, identified by IP,
  MAC address (from the ARP table), vendor (derived from the MAC), and
  hostname (reverse DNS aimed at the gateway, with an mDNS `.local` fallback)
  where available, with query counts and last-seen times. From here you can
  label
  a device, block its DNS entirely, or assign it to a **group**:
  - `filter` groups get the default rules *plus* the group's own extra
    allow/deny domains and blocked services (a group's allowed domain beats a
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
- **Blocking → Blocklists** — add, enable/disable, or remove list
  subscriptions, refresh them on demand or set how often they update
  ("Check for updates every", default 24h), with per-list rule counts and
  fetch errors. The **Choose your blocklists** catalog offers curated,
  verified lists (Hagezi, OISD, StevenBlack, threat feeds) in four plain
  tiers — Balanced, Strict, Security, Family (see "Choosing blocklists"
  below) — each card one click to subscribe, with a size hint so you know
  what you're loading on a Pi. The manual
  add-by-URL form below it takes any list the catalog doesn't carry
  (see the YAML example in the configuration reference). A list can run
  in **audit mode**: its rules are logged as amber "would block" entries
  in the Query Log but never enforced — subscribe to a strict list, watch a
  week of would-be blocks break nothing, then untick Audit to enforce it.
  The one dry-run mode Pi-hole users have asked for for years. The
  **Blocked services** card blocks a whole service
  (TikTok, YouTube, Discord…) with one checkbox for every device. To block
  a service only for some devices, or only at certain times, set it on a
  **group** on the Devices page instead — a group's blocked services apply
  to its members and obey the group's schedule.
- **Blocking → Allowed** and **Blocking → Blocked** — your own rules,
  which beat the blocklists: domains you always allow or always block
  (with their subdomains), and whole services, for everyone or one group.
  An entry written `/pattern/` is a **regex rule** — `/^ad[0-9]*\./`
  blocks ad1.example.com, ad22.example.net and so on — matched
  case-insensitively anywhere in the name. Regex rules also work in group
  lists and AdBlock-syntax blocklists, and Pi-hole and AdGuard Home
  imports bring theirs across. They use RE2, so a rule can never slow
  matching down catastrophically; backreferences, lookaround and Pi-hole's
  `;querytype=` option are not supported and are skipped with a reason. The **Allowed services** card fixes a
  streaming app a blocklist broke with one checkbox: allowing Netflix,
  Disney+, Prime Video (or any catalog service) always allows every domain
  it needs — including the playback and sign-in hosts on shared CDNs that
  the blocked-services bundles deliberately leave out. An allowed domain beats any
  blocked domain or blocklist; to allow a service only for certain devices, set
  it on a group on the Devices page.
- **Local DNS** — names on your own network. **Local records** are
  A/AAAA/CNAME records (wildcards like `*.home.lab` included) that Minos
  answers itself — they beat the blocklists, never leave your network, and
  address records answer reverse (PTR) lookups automatically.
  **Conditional forwarding** sends chosen domains to another resolver —
  typically `lan` or your reverse zone to the router, so DHCP hostnames
  keep resolving.
- **Logs** — what Minos itself is doing: list downloads, resolver
  failures, certificate renewals, errors. New lines stream in live; filter
  by level (everything, info and up, warnings and errors, errors only) and
  search messages and their details; **Pause** holds the view, **Copy**
  copies a line, **Download** saves everything held as a text file for a
  bug report. **Record** sets which messages Minos keeps — here and in the
  system journal — and applies at once (debug is very chatty; use it
  briefly). A red dot on **Logs** in the menu means a warning or error
  arrived since you last looked. The page holds the newest 2,000 messages
  and starts afresh when Minos restarts; the full history stays in the
  journal (`journalctl -u minos`) or `docker logs`. Credentials never
  appear: attributes named like tokens, keys or passwords, and passwords or
  `token=`/`key=` parameters inside URLs, are replaced before anything is
  written — in the journal too.
- **Settings** — in tabs (DNS, Filtering, Notifications, Device discovery,
  Access & security, System, Backup & import). Changes on any tab are
  drafts until you press **Save changes** in the bar that appears at the
  bottom (or **Discard**); a dot marks each tab with unsaved changes, and
  leaving the page asks first. Everything saved applies immediately, with
  no restart: upstream resolvers and their order, the response cache (repeat queries answered from memory —
  the dashboard shows the hit rate), blocking mode and TTL, network-wide
  Safe Search, the Firefox DoH canary and the iCloud Private Relay block
  (see Bypass resistance below), query-log retention and buffer
  size, the API token, a one-click YAML config backup, and an opt-in
  daily update check (a "vX.Y.Z available" link appears in the sidebar
  when a newer release exists — nothing is sent beyond the request).

If you set `api.token` (in the config file or from Settings), the UI and
CLI require it.

## Choosing blocklists

The catalog's tiers are not a quality ranking. Three of them are a ladder
graded by how much they might break; the fourth is a different question
entirely.

| Tier | What it is | Pick it when |
|---|---|---|
| **Balanced** | Ads, tracking and telemetry, tuned to avoid breakage | You want it to work and never think about it. Start here. |
| **Strict** | Wider nets, occasional collateral damage | You are willing to allow the odd thing in exchange for more blocking. |
| **Security** | Malware, phishing, and resolver-bypass protection | Always — these are cheap and orthogonal to ad blocking. |
| **Family** | Content you have chosen to keep off the network | You want it. Nothing here is a threat; it is a policy choice. |

**One list per tier is usually the right answer.** Two ad-blocking lists
overlap heavily: the second typically adds a fraction of its size in new
domains while costing its full size in memory. A sensible Pi setup is one
Balanced list plus a Security list, which is roughly 200k rules — well
inside budget. If you want more blocking, move *up* a tier rather than
adding a second list at the same one.

**The memory rule of thumb**: budget about 31 bytes of matcher per rule,
and keep the total under ~2 million rules on a Pi (about 83 MB resident).
Every card shows its size so you can add up before subscribing. The one
catalog entry large enough to notice on its own is OISD NSFW at ~480k.

**Try a strict list safely with audit mode.** Subscribe, tick Audit, and
its rules are recorded in the Query Log as amber "would block" entries
without ever being enforced. Watch a week; if nothing you care about
appears, untick Audit and it starts blocking for real. This is the same
mechanism permissive DNSSEC uses, and it is the honest way to answer
"would this break my household?" — rather than finding out live.

**A note on the Bypass Blocker.** It stops devices reaching public DoH,
VPN and proxy endpoints, which is what keeps a device from stepping
around Minos entirely and quietly ignoring every other list you run. It
also blocks commercial VPN providers, because that is the same set of
hostnames — so if someone in the house uses a VPN deliberately, allow it
and expect that to be a conscious exception.

The catalog is checked weekly in CI by compiling every list through the
real parser, so a list that is deleted, repurposed, emptied, reformatted,
or has drifted from its advertised size fails the build rather than
quietly leaving you subscribed to nothing.

**Lists survive outages and reboots.** Minos keeps the last good copy of
every list in `lists-cache/` beside its config file (`/var/lib/minos` for
the systemd service, `/data` in Docker) and compiles those copies before
it fetches anything, so blocking is on from the first second after a
restart, even if a list's host is down or the network isn't up yet. A
refresh asks the publisher whether the list changed and downloads it only
when it has; an unchanged list costs a single small request. The copies
are rewritten only when their content changes, which spares SD cards.
`querylog.ephemeral: true` keeps this cache in memory only, because that
mode promises no disk writes.

**A failing list never takes its rules away.** If an update fails — the
network is down, the host errors, a captive portal answers with a web page,
or the publisher retires the list — Minos keeps blocking with the last good
copy and says so on the Blocklists page: *Update failing · still blocking with the
copy from 3 hours ago*. Network trouble is retried within minutes (backing
off to hourly), not at the next daily refresh. A download that suddenly has
less than half the rules it had is refused, because that is far more often
a broken publish than a real change; if the same smaller list is served
three refreshes in a row, Minos accepts it as the list's new size.

**Catalog lists come with mirrors.** Hagezi lists fall back to jsDelivr and
Codeberg, StevenBlack to jsDelivr, URLhaus to its GitHub Pages copy — each
verified weekly to serve the same rules as the original. A mirror is only
used when the original is gone, refuses the download, or the copy Minos
holds has gone stale; a short outage keeps the fresher copy you already
have, because mirrors can lag behind. The Blocklists page marks a list served by a
mirror, with the reason on hover. Add `mirrors:` under any list in
`minos.yaml` to give your own lists fallbacks.

## Bypass resistance — no side doors

A sinkhole that devices can quietly sidestep is theatre. Modern browsers
and operating systems ship their own encrypted DNS, and a hardcoded
`dns.google` in an app ignores your DHCP settings entirely. Minos closes
the side doors in three layers, each visible and allowable in the query
log like any other rule:

- **Firefox stays put (on by default).** Before enabling its built-in
  DNS-over-HTTPS, Firefox asks the network first by resolving
  `use-application-dns.net`. Minos answers NXDOMAIN — Mozilla's documented
  signal that this network filters DNS — so Firefox keeps using Minos.
  No hijacking, no breakage: it is the exact mechanism Mozilla built for
  filtering resolvers. Opt out with `dns.allow_firefox_doh: true` (or the
  Settings toggle) if you'd rather Firefox decide.
- **iCloud Private Relay (opt-in).** Apple routes Safari traffic and DNS
  through its relay, hiding queries from Minos. Denying the
  `mask.icloud.com` hostnames is Apple's own documented switch: devices
  show "Private Relay is unavailable on this network" and fall back to
  normal DNS. It is a real privacy trade, so it ships off — enable it in
  Settings (`blocking.block_icloud_private_relay: true`) if judging every
  device matters more to you than Apple's relay.
- **Hardcoded DoH/DoT apps (blockable service).** The services catalog
  carries an **Encrypted DNS bypass** entry: the public DoH/DoT provider
  hostnames (Cloudflare, Google, Quad9, AdGuard, NextDNS, …) that apps and
  smart TVs hardcode. Block it network-wide from Blocking → Blocked, or — the sharp
  use — only for a kids' group, on that group's schedule, like any other
  service. Devices that can't resolve `dns.google` fall back to the DNS
  you handed them: Minos.

Minos's own upstream forwarding is unaffected by all three: upstream
exchanges never pass the filter, and the shipped resolver presets reach
DoH by IP literal. The one self-sabotage case — you hand-typed a
*hostname* DoH/DoT upstream and then blocked the encrypted-dns service —
is caught by config validation with a logged warning.

None of this stops a determined adult with a VPN. It stops the defaults:
browsers deciding for themselves, Apple relaying around you, and apps
with a resolver in their pocket.

## DNSSEC validation

By default Minos delegates DNSSEC to the upstream resolvers (Cloudflare
and Quad9 validate, and DoH/DoT protects the path to them). Turn on
`dns.dnssec` and Minos checks signed answers against the DNS root of
trust itself — so even the upstream resolver cannot forge an answer for
a signed domain:

- **permissive** — validate every forwarded answer, record the outcomes,
  never block. The right first step: run it for a while and read the
  DNSSEC card on the Dashboard (below).
- **enforce** — additionally refuse answers that *fail* validation
  (bogus: a forged record, a stripped signature, a broken chain) with
  SERVFAIL. The refusal shows in the query log attributed to `dnssec`,
  with the reason, like any other judgment.

Two deliberate safety properties: answers that cannot be judged either
way — an upstream that returns no DNSSEC records, a transient chain
fetch failure — always pass, in both modes, so a misconfigured upstream
degrades visibility rather than resolution; and unsigned zones (most of
the internet) are unaffected. Validation costs one signature check per
cache-missed answer (~50 µs on amd64, well under a millisecond on a
Pi); cache hits and blocked queries cost nothing extra. Clients that set
the DO bit get the full DNSSEC records and a trustworthy AD bit;
everyone else gets clean, stripped answers.

### Reading permissive mode before you enforce

Permissive mode exists to answer one question: *what would break if I
turned on enforce?* The DNSSEC card on the Dashboard answers it directly.

**"N answers would have been refused in enforce mode"** is the headline,
over the last 24 hours, and it links to those exact queries in the
Query Log. Each listed domain is one that enforce would have turned into a
failed lookup. Work through them before switching:

- A domain you do not recognise, or clearly do not need, is a free win —
  enforce would refuse a forged answer for it.
- A domain you *do* depend on means the zone's owner has a broken
  signature, not that Minos is wrong. Enforcing would break it for you
  until they fix it. That is the trade-off to make consciously, which is
  the whole point of looking first.

A quiet card — nothing failing over a representative period, ideally a
week including a working day — is the signal that enforce is safe to
turn on.

Beneath the headline sit four counters, **since the last restart**
(unlike the headline, they are not windowed):

| Counter | Meaning |
|---|---|
| **Verified** | Signed, and the signature checked out. |
| **Unsigned** | Provably not signed. Normal — most of the internet is unsigned. |
| **Failed** | Signed, but the signature did not verify. These are the would-be refusals. |
| **Not checkable** | No DNSSEC records came back at all. These always pass, in either mode. |

**Each counter is a link.** Clicking one opens the Query Log filtered to the
queries with that outcome, so "286 unsigned" becomes a list of which
domains those actually were. Two things to expect: the counters are
process-lifetime while the Query Log covers the retained log, so the totals
will not tally; and because validation runs on resolution, a name answered
from the cache adds to neither.

If **Not checkable** dominates, the card says so explicitly, and it
matters: it means your upstream resolver is not returning DNSSEC records,
so validation is doing almost nothing no matter which mode you pick. A
card that is quiet for that reason looks identical to one that is quiet
because everything is fine. Point `dns.upstreams` at a resolver that
supports DNSSEC (the defaults do) and the counters should start moving.

One honest caveat about the numbers: **the headline counts resolutions,
not lookups.** Validation runs when an answer is actually fetched from
upstream, so a domain answered from the response cache is not
re-validated and does not add a row. The Query Log therefore shows fewer
would-block rows than the "Failed" counter, sometimes far fewer on a busy
network. Both are correct; they measure different things.
## Host health

The Dashboard carries a **Host** card describing the machine Minos runs
on: CPU, load, memory, disk, temperature, uptime, and how much memory
Minos itself is using. It refreshes every 15 seconds.

The disk figure is deliberately not the biggest filesystem — it is the
one holding the **query log**. On a Pi that is usually the SD card, and
it is the number most likely to actually cause trouble: when it fills,
Minos stops recording. Past 90% the card says so and turns red. The
remedy is to lower `querylog.retention_days`, or move `querylog.db_path`
to other storage.

**Not everything is measurable everywhere, and Minos will not pretend
otherwise.** A reading the platform cannot supply shows an em dash, with
a hover saying so — never a zero, which would read as an idle, healthy
machine:

| Reading | Where it works |
|---|---|
| CPU, memory, disk, uptime | Linux, Windows |
| Load average | Linux, macOS — Windows has no such concept |
| CPU utilisation | Linux, Windows — macOS needs a call unavailable in a static build |
| Temperature | Linux where the hardware exposes a sensor, including the Pi |

**Container installs report no host health at all**, and the card does
not appear. This is deliberate. Inside a container the question "how is
the machine running Minos doing?" has two possible answers and both are
wrong: the host's figures describe a machine you may not administer and
whose CPU and disk Minos does not have to itself, while the container's
own limits are not the host. A number confidently about the wrong
subject is worse than no number, so Docker installs get no card. Use
your container platform's own metrics for that layer.

If no API token is set, the card points that out. It is the first part
of the UI to expose the machine's identity rather than DNS activity, so
it is worth deciding consciously — see `api.token` in the configuration
reference.

## Monitoring with Prometheus / Grafana

`GET /metrics` on the API port serves Prometheus exposition format:
query/block counters, cache hit rate, per-upstream request counts,
failures, and cumulative latency, per-list rule counts, pause state, and
(when `dns.dnssec` is on) validation outcomes as
`minos_dnssec_results_total{status="secure|insecure|bogus|indeterminate"}`.
On a non-container install it also exports host health as
`minos_host_cpu_percent`, `minos_host_load1`,
`minos_host_memory_bytes{state=…}`, `minos_host_disk_bytes{state=…}`,
`minos_host_temperature_celsius`, and `minos_process_resident_bytes`.
A metric this platform cannot measure exports **no series at all**
rather than a zero — a zero would graph as a healthy idle machine when
the truth is that nobody measured it.

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

A ready-made Grafana dashboard ships in `deploy/grafana-dashboard.json` —
import it (Dashboards → New → Import), pick your Prometheus datasource,
and you get block rate, cache behaviour, per-upstream latency and health,
and per-list rule counts out of the box.

Scripting Minos instead of watching it? The full REST surface is in
[api.md](api.md), and [home-assistant.md](home-assistant.md) has
copy-paste recipes: a blocking switch, sensors, bedtime device blocks,
and Minos events on your phone.

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

Prefer the web UI? **Settings → Import from Pi-hole or AdGuard Home** takes
the same files as an upload (`gravity.db`, an optional `custom.list`, or
`AdGuardHome.yaml`) and shows the same add-only report — no shell needed.
The **Backup & restore** card there also restores a `minos.yaml` you
exported earlier (listen addresses and query-log storage are kept from the
running instance, since those need a restart to change).

## Configuration

Everything lives in one YAML file and every setting can be changed through
the API or Settings page without a restart (the listen addresses —
including `dns.tls` — and the query-log storage location are the
exceptions; those are file-only). Key sections:

```yaml
dns:
  listen: ":53"
  upstreams:                # tried in order; the first is the primary
    - address: https://1.1.1.1/dns-query   # IP-literal URL, not a hostname:
      protocol: doh         # a resolver shouldn't have to resolve its own
                            # resolver first. Cloudflare's cert lists 1.1.1.1
                            # as an IP SAN, so TLS still validates.
    - address: 1.0.0.1:853  # DoT fallback
      protocol: dot         # udp | tcp | dot | doh
  cache:                    # answer repeat queries from memory
    enabled: true
    max_entries: 10000      # ~500 B per entry
    min_ttl: 10             # seconds; keep short-lived answers at least this long
    max_ttl: 3600           # never serve a cached answer longer than this
    serve_stale: true       # RFC 8767: answer from an expired entry (up to
                            # 6h old) and refresh in the background
  dnssec: off               # validate forwarded answers against the DNSSEC
                            # chain of trust: off | permissive | enforce.
                            # permissive validates and records, never blocks;
                            # enforce refuses forged (bogus) answers with
                            # SERVFAIL. Answers that cannot be judged either
                            # way always pass, so an upstream without DNSSEC
                            # support degrades visibility, never resolution.
                            # Run permissive first and read the Dashboard's
                            # DNSSEC card: it names what enforce would refuse.
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
    cert_file: /var/lib/minos/fullchain.pem   # or an acme: block — see above
    key_file: /var/lib/minos/privkey.pem
    dot_listen: ":853"      # DNS-over-TLS (Android Private DNS); empty = off
    doh_listen: ":8443"     # DNS-over-HTTPS at /dns-query (+ HTTP/3 on UDP); empty = off
    doq_listen: ":853"      # DNS-over-QUIC (UDP); empty = off
blocking:
  mode: zero_ip             # or nxdomain
  allowlist: []             # always allowed (with subdomains)
  denylist: []              # always blocked (with subdomains)
  services: [onlyfans]      # curated service bundles, blocked for everyone
  allowed_services: [netflix]  # service bundles allowed for everyone (allow beats deny)
  custom_services:          # your own service bundles (see below)
    - name: my-game         # lowercase slug; must not shadow a catalog name
      label: My Game
      domains: [mygame.example]
      allow_extra: [login.mygame.example]  # allowed only while the service is allowed
      blocked: true         # global block toggle (lives here, not in services:)
      allowed: false        # global allow toggle
  safe_search: false        # rewrite search engines to enforced-safe variants
groups:                     # device policies (all optional)
  - name: kids
    mode: filter            # filter | bypass | block
    denylist: [tiktok.com]  # extra blocks for members only
    services: [snapchat]    # service bundles for members only
    allowed_services: []    # service bundles allowed for members only
    custom_services: []     # custom bundles blocked for members only
    allowed_custom_services: []  # custom bundles allowed for members only
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
      mirrors:              # optional fallbacks, used only if the main URL is
        - https://cdn.jsdelivr.net/gh/StevenBlack/hosts@master/hosts  # gone or stale
    - name: abuse.ch keyed feed
      url: https://example.abuse.ch/feed.txt   # a feed that needs a key
      format: hosts
      enabled: true
      headers:              # sent to this URL only — never to mirrors, never
        Auth-Key: "your-key" # across a redirect to another host, never via the API
  allow_sources:            # subscribed allowlists: every entry is always
    - name: unbreak         # allowed, beating any blocklist — an allowlist you
      url: https://example.com/commonly-white.txt   # subscribe to
      format: plain
      enabled: true
  refresh_interval: 24h
querylog:
  ephemeral: false          # true = never touch disk
  db_path: minos.db
  ring_size: 10000
  retention_days: 90
  memory_mb: 32             # RAM cap for the query log database (8–4096)
api:
  listen: 0.0.0.0:8080
  token: ""                 # set one if the LAN isn't fully trusted
update_check: false         # opt-in: ask GitHub for the latest release once
                            # a day; nothing is sent beyond the request
                            # itself. Release builds only — a -dev build
                            # never checks.
notifications:              # optional; nothing is sent unless a URL is set
  webhook_url: ""           # each event POSTed as JSON
  ntfy_url: ""              # e.g. https://ntfy.sh/my-topic (or self-hosted)
  ntfy_token: ""            # bearer token for protected topics
  digest: off               # off | daily | weekly: a traffic summary
  digest_time: "09:00"      # delivery time, 24h HH:MM, server time
  digest_day: monday        # delivery day (weekly only), lowercase
  ui_url: ""                # where tapping a notification goes; blank = derived
  quiet_hours: ""           # e.g. "22:00-07:00": deliver silently overnight
  mute: []                  # event types never sent, e.g. [digest, device_new]
```

Every notification says what happened, why it matters and what to do, and
**tapping it opens the right page in Minos** — the new device's page, the
Blocklists page, resolver settings. Links use `ui_url`, or when that is
blank, the API's listen address (this machine's LAN address when Minos
listens on every interface); Settings shows the address it will use. Set
it if you reach Minos by another name.

| Event | When | Priority |
|---|---|---|
| New device | a device makes its first DNS query — sent ~90 s later so it can be named, and a burst (a router reboot) arrives as one message; quiet for 5 minutes after Minos starts | default |
| Blocklist problem | a list is retired (404/410) or refuses downloads (401/403) at once; never loaded or stale after three failed attempts | high (stale: default) |
| Resolver not answering | an upstream trips the failure breaker while others still answer; a repeat within 30 minutes stays quiet, but is re-checked and reported if it stays down | default |
| **DNS is down** | every upstream has stopped answering, so nothing resolves | urgent, ignores quiet hours |
| Recovery | a list or resolver that was reported failing works again | low |
| Minos update | a new release is out (update check on) | low |
| Certificate problem | the DoT/DoH certificate cannot be renewed | high |
| Digest | the opt-in daily or weekly summary: totals, block rate, top blocked, busiest device, new devices | low |

Each can be switched off in Settings → Notifications (or with `mute`),
except "DNS is down". **Quiet hours** deliver everything silently (ntfy
priority 2) instead of dropping it. **Send test notification** delivers one
straight away and reports, per destination, whether it arrived or why not.
ntfy messages are published as JSON, so titles and bodies keep their
punctuation, and carry the link as the tap action plus an "Open …" button.
Webhook payloads look like:

```json
{"type":"device_new","title":"New device: Kitchen speaker",
 "message":"Kitchen speaker made its first DNS query through Minos (made by Sonos, address 192.168.1.77, MAC aa:bb:cc:dd:ee:ff). If you don't recognise it, open it in Minos to name it, put it in a group, or block it.",
 "time":"2026-07-05T10:12:03Z",
 "url":"http://192.168.1.2:8080/#/device/aa:bb:cc:dd:ee:ff",
 "priority":3,
 "actions":[{"label":"Open device","url":"http://192.168.1.2:8080/#/device/aa:bb:cc:dd:ee:ff"}]}
```
