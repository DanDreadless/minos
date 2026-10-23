# Minos user guide

<!-- Generated from web/src/lib/docs.ts by web/scripts/gen-user-guide.mjs — edit that file, not this one. -->

The same guide is built into Minos: open **Help** in the menu, or the **?** beside any page title.

"Applies at once" means the setting can be changed from the web interface (or the API) and takes effect
immediately. "After restart" settings live only in `minos.yaml`; restart Minos after editing the file.

## Contents

- [Getting started](#start)
- [Dashboard](#dashboard)
- [Query Log](#querylog)
- [Devices](#devices)
- [Blocking → Blocklists](#blocklists)
- [Blocking → Allowed and Blocked](#allowblock)
- [Local DNS](#localdns)
- [Logs](#logs)
- [Settings → DNS](#settings-dns)
- [Settings → Filtering](#settings-filtering)
- [Settings → Notifications](#settings-notifications)
- [Settings → Device discovery](#settings-discovery)
- [Settings → Access & security](#settings-access)
- [Settings → System](#settings-system)
- [Settings → Backup & import](#settings-backup)
- [Command line](#cli)

<a id="start"></a>

## Getting started

Minos is a DNS server for your home network that blocks ads, trackers and malware before they load. Every device that uses it as its DNS server has each lookup checked against your blocklists and your own rules: blocked names get an empty answer, everything else is looked up through your upstream resolvers.

- Point devices at Minos: set your router's DHCP DNS option to this machine's address, so every device follows, or set the DNS server on individual devices.
- Choose blocklists on Blocking → Blocklists — the catalog has curated picks; Balanced is the recommended start.
- Watch the Query Log for a day. If something you use breaks, find its blocked lookup and press Allow.
- Optional: set an API token (Settings → Access & security), notifications, and the update check.

**What is at the bottom of the side menu?** Whether Minos is blocking right now ("Blocking · 73k rules", "Blocking paused until 14:20", or in red "Not blocking — no blocklist rules loaded"), Pause blocking, and Check a domain (also Ctrl+K / ⌘K on any page): paste a name or URL to see whether it is blocked and why, then allow or block it. On a phone, a paused or not-blocking state also shows in the top bar.

**Something broke and I think Minos blocked it.** Open the Query Log, filter by the device or type the site's name, and look for Blocked rows around the time it broke. Press Allow on the row (or use Check a domain, Ctrl+K). Allowed domains include their subdomains.

**How do I turn blocking off for a moment?** Pause blocking at the bottom of the side menu: 5 or 30 minutes, an hour, until you resume, or any duration such as 2h. Device-level blocks (Devices → Block DNS) still apply while paused.

<a id="dashboard"></a>

## Dashboard

What your network did over a window you choose: queries, blocked, block rate, a volume chart, the most-blocked domains and the busiest devices — every number for the same window.

- Pick the window at the top (24 hours, 7, 30 or 90 days). It is remembered in this browser.
- Click any number, domain or device to open exactly those queries in the Query Log.
- Needs attention appears only when something does: no rules loaded, a list failing or retired, a resolver not answering, no API token, a nearly full disk, DNSSEC unable to check answers, or new warnings in Logs. Each line links to its fix.
- The line under the tiles gives totals since Minos last started; the cache hit rate is also since the last start.
- The Host card (not shown in containers) reports the machine Minos runs on; the disk figure is the filesystem holding the query log.
- The DNSSEC card appears when validation is on: in permissive mode it lists answers that enforce mode would have refused.

<a id="querylog"></a>

## Query Log

Every DNS lookup your devices make — streaming live, and searchable across the whole kept history — with the list and rule behind every block.

- With no filter the page is Live: new lookups arrive at the top. Pause holds the rows still while you read; new ones wait, counted.
- Any filter switches to Results: matches from the full history, newest first, with Load older.
- Filter with the result buttons (All, Blocked, Allowed, Would block), the list menu and the time range — or click any device, domain, type, list or resolver in a row.
- The search box takes filters as you type: blocked, allowed, would-block, client:192.168.1.5, domain:example.com (that exact name), type:AAAA, list:StevenBlack (quote names with spaces), upstream:1.1.1.1:853, dnssec:bogus, and -cdn to leave out names containing "cdn". Anything else matches domains and device addresses. Press / to search, Enter to apply, Esc to clear, Backspace to remove the last filter.
- Row actions: Allow (on blocked rows) or Block (on allowed rows), Check (how the domain would be judged now) and Copy.
- Export CSV or JSON downloads every match of the current filter, up to 100,000 rows.
- The address bar keeps the filter, so a search can be bookmarked or shared.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Keep history (days)<br><small>Settings → System</small> | `querylog.retention_days` | 90 | How long lookups are kept on disk. Older rows are removed once a day. | at once |
| Recent queries kept in memory<br><small>Settings → System</small> | `querylog.ring_size` | 10000 | How many of the newest lookups are held in memory for the live view. History on disk is unaffected. | at once |
| Query log memory limit (MB)<br><small>Settings → System</small> | `querylog.memory_mb` | 32 | The most RAM the query-log database uses for caching and for working out searches and statistics (8–4096). Bigger jobs spill to a temporary file instead of using more memory. | at once |
| Query log file<br><small>minos.yaml</small> | `querylog.db_path` | minos.db (installers put it in /var/lib/minos) | Where history is stored. | after restart (minos.yaml) |
| Memory-only mode<br><small>minos.yaml</small> | `querylog.ephemeral` | false | Keeps no history on disk at all (and keeps the blocklist cache in memory): the Query Log and Dashboard only cover what is in memory since the last start. | after restart (minos.yaml) |

<a id="devices"></a>

## Devices

Every device that has used Minos, named where Minos could find out (router names, mDNS, NetBIOS, UPnP, DHCP), with groups that give devices different rules and schedules.

- Give a device a label, put it in a group, or Block DNS to refuse all of its lookups (this applies even while blocking is paused).
- Open a device for its page: every address it has used, what it looked up most, its full query history and notes.
- A device is followed by its MAC address when Minos can see one, so its rules survive a new IP address from DHCP.
- Groups: filter (your normal rules plus the group's own allowed/blocked domains and services), bypass (no filtering) or block (no DNS). A group can have a schedule — it applies only during those hours, and devices follow the normal rules outside them.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Devices<br><small>Devices</small> | `clients` | — | Saved labels, groups, blocks and notes for devices.<br>`ip`: The device's address (its last known one when a MAC is set).<br>`mac`: When set, the device is recognised by MAC on any address.<br>`name`: Your label for it.<br>`group`: The group it belongs to (empty: the normal rules).<br>`blocked`: Refuse all of its DNS lookups.<br>`notes`: Free text, up to 4096 characters. | at once |
| Groups<br><small>Devices → Groups</small> | `groups` | — | Named policies for sets of devices.<br>`name`: The group's name.<br>`mode`: filter, bypass or block.<br>`allowlist`: Extra allowed domains for members.<br>`denylist`: Extra blocked domains for members.<br>`services`: Services blocked for members.<br>`allowed_services`: Services allowed for members.<br>`custom_services`: Your custom services blocked for members.<br>`allowed_custom_services`: Your custom services allowed for members.<br>`safe_search`: Enforce Safe Search for members.<br>`schedule`: When the group applies; outside it, members follow the normal rules.<br>`schedule.days`: Days as mon, tue … sun (empty: every day).<br>`schedule.start`: Start time, HH:MM server time.<br>`schedule.end`: End time; before the start means it runs past midnight. | at once |

<a id="blocklists"></a>

## Blocking → Blocklists

The lists Minos blocks from, downloaded and kept up to date automatically. If a list's server is down, Minos keeps blocking with the last good copy (also after a restart).

- Choose your blocklists: the catalog offers verified lists in tiers — Balanced (recommended), Strict (more blocking, more to allow), Security (malware and phishing) and Family (content you choose to keep off the network). One click subscribes.
- Each list shows a plain status — up to date, failing but still blocking with the copy from a given time, retired, and so on. Hover for the reason.
- Audit: tick it to try a list without enforcing it — its matches are recorded as "would block" in the Query Log. Untick to enforce.
- Allowlists (subscribed lists of always-allowed domains) can be added too; they beat every blocklist.
- "Replace with…" appears on a list its publisher has retired; "Add mirrors" on catalog lists subscribed before mirrors existed.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Check for updates every<br><small>Blocking → Blocklists</small> | `lists.refresh_interval` | 24h | How often every list is downloaded again (minimum 5m). A list that fails is retried sooner, backing off from a minute to an hour. | at once |
| Blocklists<br><small>Blocking → Blocklists</small> | `lists.sources` | — | Subscribed blocklists.<br>`name`: A unique name, shown in the Query Log.<br>`url`: Where it is downloaded from.<br>`format`: hosts, adblock or plain (one domain per line).<br>`enabled`: Off keeps the subscription but stops using it.<br>`audit`: Record matches as "would block" without blocking.<br>`mirrors`: Up to 5 fallback addresses, used only when the main one is gone or the copy is getting old.<br>`headers`: Extra request headers, e.g. an access key for a keyed feed. Config file only; never shown by the API and never sent to a mirror. | at once |
| Allowlists<br><small>Blocking → Blocklists</small> | `lists.allow_sources` | — | Subscribed lists of always-allowed domains; same fields as blocklists.<br>`name`: A unique name.<br>`url`: Where it is downloaded from.<br>`format`: hosts, adblock or plain.<br>`enabled`: Off stops using it.<br>`audit`: Not allowed on allowlists (a validation error).<br>`mirrors`: Fallback addresses.<br>`headers`: Extra request headers (config file only). | at once |

<a id="allowblock"></a>

## Blocking → Allowed and Blocked

Your own rules, which beat the blocklists: domains you always allow or always block, and whole services (every domain an app uses) in one move.

- Allowed and Blocked are separate tabs; each has your domains (as chips — × removes one) and the services for everyone or for one group.
- Allowed and blocked domains include their subdomains. An allowed domain beats any blocked domain or blocklist.
- Services: block or allow a whole service — for everyone, or for one group (choose it under "Services for"). Allowing a streaming service also allows the playback and sign-in hosts it needs.
- Your custom services: define your own bundle of domains, then block or allow it like any service.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Allowed domains<br><small>Blocking → Allowed</small> | `blocking.allowlist` | — | Always allowed, with subdomains. An entry written /pattern/ is a regex rule (case-insensitive, matched anywhere in the name): an allow regex beats any block. | at once |
| Blocked domains<br><small>Blocking → Blocked</small> | `blocking.denylist` | — | Always blocked, with subdomains. An entry written /pattern/ is a regex rule, e.g. /^ad[0-9]*\./ — checked after the exact rules, so an allowed domain still wins. Regexes use RE2 (no backreferences or lookaround); up to 1000 per list. | at once |
| Blocked services<br><small>Blocking → Allowed / Blocked → Services</small> | `blocking.services` | — | Services blocked for everyone. | at once |
| Allowed services<br><small>Blocking → Allowed / Blocked → Services</small> | `blocking.allowed_services` | — | Services allowed for everyone; allowing beats blocking. | at once |
| Your custom services<br><small>Blocking → Allowed / Blocked → Your custom services</small> | `blocking.custom_services` | — | Your own service bundles.<br>`name`: Its ID (derived from the label if not given).<br>`label`: Its display name.<br>`domains`: The domains it covers.<br>`allow_extra`: Extra hosts allowed only while the service is allowed.<br>`blocked`: Blocked for everyone.<br>`allowed`: Allowed for everyone. | at once |

<a id="localdns"></a>

## Local DNS

Names on your own network: records Minos answers itself, and domains it sends to another resolver — usually your router, so device names from DHCP keep working.

- Local records: A (IPv4), AAAA (IPv6) or CNAME (alias). A leading *. matches all subdomains. Address records also answer reverse (PTR) lookups. Local records beat the blocklists and never leave your network.
- Conditional forwarding: send domains such as lan, home.arpa or your reverse zone (1.168.192.in-addr.arpa) to the router. A route is final: if its resolver fails, the lookup fails rather than going upstream.
- Lookups for private address ranges that no route covers are answered "not found" locally rather than leaking upstream.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Local records<br><small>Local DNS</small> | `dns.local_records` | — | Names Minos answers itself.<br>`name`: The name, e.g. nas.home.lab or *.home.lab.<br>`a`: IPv4 addresses.<br>`aaaa`: IPv6 addresses.<br>`cname`: An alias target (instead of addresses). | at once |
| Local record TTL<br><small>minos.yaml or the API</small> | `dns.local_ttl` | 300 | How long devices cache a local answer (seconds). | at once |
| Conditional forwarding<br><small>Local DNS</small> | `dns.routes` | — | Domains sent to a different resolver.<br>`domains`: Domains it covers, with their subdomains.<br>`upstream`: The resolver to use.<br>`upstream.address`: host:port, or a URL for DoH.<br>`upstream.protocol`: udp, tcp, dot, doh, doh3 or doq. | at once |
| Forward private reverse lookups<br><small>minos.yaml</small> | `dns.forward_private_reverse` | false | Send reverse lookups for private addresses upstream instead of answering them locally. | after restart (minos.yaml) |

<a id="logs"></a>

## Logs

Minos's own activity — list downloads, resolver trouble, certificate renewals, errors — live. A red dot on Logs in the menu means a warning or error you haven't seen.

- Filter by level and search messages and their details; Pause holds the view; Copy copies a line; Download saves everything held as a text file for a bug report.
- The page holds the newest 2,000 messages and starts afresh when Minos restarts; the full history is in the system journal (journalctl -u minos) or docker logs.
- Tokens, keys and passwords never appear — not here and not in the journal.
- From a shell: minos logs -f -level warn.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Application log level<br><small>Settings → System, or Record on the Logs page</small> | `log_level` | info (or the -log-level flag) | Which messages Minos records, here and in the system journal: debug, info, warn or error. Debug records every query — use it briefly. | at once |

<a id="settings-dns"></a>

## Settings → DNS

Where Minos looks up allowed names, whether it checks DNSSEC, and its answer cache.

- Upstream resolvers are tried in order: the first is the primary. The light beside each is green when it is answering, red when failing, hollow when it has not been needed yet.
- Encrypted protocols (DoH, DoT) keep your lookups private from your internet provider; the presets are verified to work.
- Changes on every Settings tab are drafts until you press Save changes in the bar at the bottom.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Upstream resolvers<br><small>Settings → DNS</small> | `dns.upstreams` | Cloudflare: https://1.1.1.1/dns-query (DoH), then 1.0.0.1:853 (DoT) | Where allowed lookups are sent, in order. A resolver that fails three times in a row is skipped for 30 seconds, then retried.<br>`address`: host:port for udp, tcp, dot and doq; an https:// URL for doh and doh3.<br>`protocol`: udp, tcp, dot (DNS over TLS), doh (DNS over HTTPS), doh3 (DNS over HTTPS on HTTP/3) or doq (DNS over QUIC). | at once |
| DNSSEC validation<br><small>Settings → DNS</small> | `dns.dnssec` | off | off trusts the resolvers; permissive checks signed answers and records failures without refusing anything; enforce refuses answers that fail. Start with permissive. | at once |
| Response cache<br><small>Settings → DNS</small> | `dns.cache` | — | Answers repeat lookups from memory instead of asking upstream. Blocked answers are never cached.<br>`enabled`: On by default.<br>`max_entries`: How many answers to keep (default 10000).<br>`min_ttl`: Keep short-lived answers at least this long, in seconds (default 10).<br>`max_ttl`: Never serve from cache longer than this, in seconds (default 3600).<br>`serve_stale`: If upstream is slow or down, answer from an expired entry (up to 6 hours old) while fetching a fresh one (default on). | at once |
| DNS listen address<br><small>minos.yaml</small> | `dns.listen` | :53 | Where Minos accepts DNS lookups. | after restart (minos.yaml) |
| Encrypted DNS for your devices (DoT/DoH)<br><small>minos.yaml</small> | `dns.tls` | — | Lets devices reach Minos itself over DNS over TLS and DNS over HTTPS. Needs a certificate: files, or one issued automatically (ACME, DNS-01). With either on, Minos also advertises them over DDR, so Windows 11, iOS and macOS can switch to encrypted DNS by themselves — when the certificate covers the address they use for Minos.<br>`dot_listen`: Address for DNS over TLS, usually :853 (empty: off).<br>`doh_listen`: Address for DNS over HTTPS at /dns-query, usually :443 (empty: off). HTTP/3 is served on the same port over UDP and advertised, so clients that speak it upgrade.<br>`doq_listen`: Address for DNS over QUIC (RFC 9250), usually :853 — UDP, so it can share the number with DoT (empty: off).<br>`cert_file`: Certificate file (instead of acme).<br>`key_file`: Its private key.<br>`acme`: Issue and renew a certificate automatically.<br>`acme.email`: Contact address for the certificate authority.<br>`acme.domain`: The name the certificate is for.<br>`acme.provider`: cloudflare, desec, duckdns or rfc2136 — who publishes the DNS-01 record.<br>`acme.api_token`: API token for cloudflare, desec or duckdns.<br>`acme.server`: RFC 2136 server, host:port.<br>`acme.tsig_name`: RFC 2136 TSIG key name.<br>`acme.tsig_secret`: RFC 2136 TSIG secret.<br>`acme.tsig_algorithm`: RFC 2136 TSIG algorithm (default hmac-sha256).<br>`acme.directory_url`: ACME directory (default Let's Encrypt).<br>`acme.cache_dir`: Where issued certificates are kept. | after restart (minos.yaml) |

<a id="settings-filtering"></a>

## Settings → Filtering

How blocked lookups are answered, and the protections against devices working around Minos.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Block response<br><small>Settings → Filtering</small> | `blocking.mode` | zero_ip | zero_ip answers 0.0.0.0 / :: (fails fast and quietly — recommended); nxdomain says the domain does not exist. | at once |
| Remember blocks for (seconds)<br><small>Settings → Filtering</small> | `dns.block_ttl` | 60 | How long a device caches a blocked answer before asking again. Lower makes an Allow take effect sooner. | at once |
| Enforce Safe Search for everyone<br><small>Settings → Filtering</small> | `blocking.safe_search` | off | Google, Bing, DuckDuckGo and YouTube (moderate) answer with their safe versions. Bypass groups are exempt. | at once |
| Keep Firefox on this resolver<br><small>Settings → Filtering</small> | `dns.allow_firefox_doh` | false (Firefox is kept on Minos) | When false, Minos tells Firefox this network filters DNS, so Firefox does not switch itself to its own encrypted DNS. Untick "Keep Firefox on this resolver" to set it true. | at once |
| Block trackers hidden behind CNAMEs<br><small>Settings → Filtering</small> | `blocking.cname_inspection` | on | Some trackers hide behind a name that looks first-party (metrics.shop.example) but is an alias (CNAME) for a tracker. Minos checks every alias in an answer against your rules and blocks the answer when one is blocked; the Query Log shows "via CNAME <tracker>". Names you have allowed, and bypass devices, are not checked. | at once |
| Protect against DNS rebinding<br><small>Settings → Filtering</small> | `dns.rebind_protection` | off | Refuses answers that point a public name at an address on your own network (192.168.x.x, 10.x.x.x, fd00::/8, loopback) — the trick a malicious web page uses to reach your router or NAS. Allowed domains (e.g. plex.direct for Plex), local records, conditional forwarding and names like .lan or .home.arpa are exempt. Refusals show in the Query Log under the list "rebinding". | at once |
| Block iCloud Private Relay<br><small>Settings → Filtering</small> | `blocking.block_icloud_private_relay` | off | Apple devices fall back to normal DNS so Minos can filter them. A real privacy trade-off. | at once |

<a id="settings-notifications"></a>

## Settings → Notifications

Messages to your phone (ntfy) or any webhook when something on your network changes. Each says what happened and what to do, and tapping it opens the right page in Minos.

- Set an ntfy topic URL (e.g. https://ntfy.sh/my-minos-topic) or a webhook, save, then Send test notification.
- "DNS is down" — every resolver failing — is always sent, at urgent priority, even in quiet hours.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Webhook URL<br><small>Settings → Notifications</small> | `notifications.webhook_url` | — | Each event is POSTed here as JSON. | at once |
| ntfy topic URL<br><small>Settings → Notifications</small> | `notifications.ntfy_url` | — | An ntfy topic — ntfy.sh or self-hosted. | at once |
| ntfy access token<br><small>Settings → Notifications</small> | `notifications.ntfy_token` | — | Only for protected topics. Never shown again once saved. | at once |
| Link address<br><small>Settings → Notifications</small> | `notifications.ui_url` | this machine's address and the API port | Where tapping a notification takes you. Set it if you reach Minos by another name. | at once |
| What to send<br><small>Settings → Notifications</small> | `notifications.mute` | — | Event types switched off. | at once |
| Quiet hours<br><small>Settings → Notifications</small> | `notifications.quiet_hours` | none | e.g. 22:00-07:00: notifications arrive silently during these hours. | at once |
| Traffic digest<br><small>Settings → Notifications</small> | `notifications.digest` | off | A daily or weekly summary of what your network did. | at once |
| Digest time<br><small>Settings → Notifications</small> | `notifications.digest_time` | 09:00 | When the digest is sent (server time). | at once |
| Digest day<br><small>Settings → Notifications</small> | `notifications.digest_day` | monday | Which day the weekly digest is sent. | at once |

<a id="settings-discovery"></a>

## Settings → Device discovery

How Minos learns the names of the devices on your network. Everything stays on your network.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Ask UPnP devices to introduce themselves (SSDP)<br><small>Settings → Device discovery</small> | `discovery.ssdp` | on | Names smart TVs, consoles and other gadgets by their own name, maker and model. | at once |
| Listen for DHCP introductions (Linux)<br><small>Settings → Device discovery</small> | `discovery.dhcp_listen` | on | Devices announce their own name when they join the network. Minos only listens — it never hands out addresses. | at once |

<a id="settings-access"></a>

## Settings → Access & security

Who can use this interface and the API.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Password<br><small>Settings → Access & security</small> | `api.password_hash` | none | When set, opening the interface asks for it, and a sign-in lasts 30 days on that browser. Five wrong tries from one address wait a minute. Stored only as a one-way hash (argon2id) — set or change it in Settings, never in the file. Forgot it? Delete password_hash from minos.yaml and restart. | at once |
| Session signing key<br><small>Settings → Access & security</small> | `api.session_key` | Made with the first password | Signs sign-ins. "Sign out everywhere" replaces it, which ends every session at once. The minos command on the same machine uses it too, so it keeps working with only a password set. | at once |
| Two-step sign-in (authenticator app)<br><small>Settings → Access & security</small> | `api.totp_secret` | off | After the password, ask for the 6-digit code from an authenticator app (Google Authenticator, 1Password, Aegis …). Turned on by scanning the code shown in Settings and typing one code back. | at once |
| API token<br><small>Settings → Access & security</small> | `api.token` | none | For scripts and automation (Home Assistant, curl): sent as X-Api-Token or Authorization: Bearer. With a password set too, either works. With neither, anyone on your network can change Minos. | at once |
| Networks Minos answers<br><small>Settings → Access & security</small> | `dns.allowed_networks` | Private and local networks | Which devices may use Minos, as networks (192.168.1.0/24) or single addresses. Blank answers private networks (192.168.x.x, 10.x.x.x, 172.16–31.x.x, 100.64.x.x, fd00::/8, link-local, loopback) and the networks this machine is connected to — enough for any home network, IPv6 included — and refuses everyone else, so an accidentally exposed port never makes Minos an "open resolver" for the internet. Refusals are counted in the Logs, not the Query Log. | at once |
| Queries a minute per device<br><small>Settings → Access & security</small> | `dns.rate_limit` | 1000 | The most lookups one device may make in a minute; beyond that it is refused until the minute is up. Stops a runaway app or an infected device from swamping Minos. The first refusal each minute appears in the Query Log under "rate-limit". 0 turns the limit off. | at once |
| Interface listen address<br><small>minos.yaml</small> | `api.listen` | 0.0.0.0:8080 | Where this web interface and the API listen. | after restart (minos.yaml) |

<a id="settings-system"></a>

## Settings → System

Query-log storage, the application log level, and updates.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Check for new releases once a day<br><small>Settings → System</small> | `update_check` | off | Asks GitHub for the latest release — nothing about you or your network is sent. A banner shows how to upgrade. | at once |
| Install method<br><small>minos.yaml</small> | `update_install_method` | detected | binary, docker or source — forces the upgrade instructions when detection cannot tell. | after restart (minos.yaml) |

<a id="settings-backup"></a>

## Settings → Backup & import

Download the whole configuration (minos.yaml) as a backup, restore one, or bring your setup across from Pi-hole or AdGuard Home. Imports only add; nothing already configured is removed.

<a id="cli"></a>

## Command line

The minos command talks to the running server through its API (using -config to find the address and token).

- minos status — counters, rules and pause state.
- minos pause 30m / minos resume — pause and resume blocking.
- minos logs -f -level warn — follow the application log.
- minos import pihole /etc/pihole or minos import adguard AdGuardHome.yaml — migrate settings.
- minos version.
