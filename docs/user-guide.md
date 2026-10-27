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
- [Privacy](#privacy)
- [Local DNS](#localdns)
- [Logs](#logs)
- [Home Assistant](#home-assistant)
- [Settings → DNS](#settings-dns)
- [Settings → Filtering](#settings-filtering)
- [Settings → Notifications](#settings-notifications)
- [Settings → Device discovery](#settings-discovery)
- [Settings → Home Assistant](#settings-homeassistant)
- [Settings → Access & security](#settings-access)
- [Settings → System](#settings-system)
- [Settings → Backup & import](#settings-backup)
- [Settings → History](#settings-history)
- [Command line](#cli)

<a id="start"></a>

## Getting started

Minos is a DNS server for your home network that blocks ads, trackers and malware before they load. Every device that uses it as its DNS server has each lookup checked against your blocklists and your own rules: blocked names get an empty answer, everything else is looked up through your upstream resolvers.

- Point devices at Minos: set your router's DHCP DNS option to this machine's address, so every device follows, or set the DNS server on individual devices.
- Choose blocklists on Blocking → Blocklists — the catalog has curated picks; Balanced is the recommended start.
- Watch the Query Log for a day. If something you use breaks, find its blocked lookup and press Allow.
- Set a password (Settings → Access & security) so only you can change Minos. Then, optionally, notifications and the update check.

**What is at the bottom of the side menu?** Whether Minos is blocking right now ("Blocking · 73k rules", "Blocking paused until 14:20", or in red "Not blocking — no blocklist rules loaded"), Pause blocking, and Check a domain (also Ctrl+K / ⌘K on any page): paste a name or URL to see whether it is blocked and why, then allow or block it. On a phone, a paused or not-blocking state also shows in the top bar.

**Something broke and I think Minos blocked it.** Open the Query Log, filter by the device or type the site's name, and look for Blocked rows around the time it broke. Press Allow on the row (or use Check a domain, Ctrl+K). Allowed domains include their subdomains.

**How do I turn blocking off for a moment?** Pause blocking at the bottom of the side menu: 5 or 30 minutes, an hour, until you resume, or any duration such as 2h. Device-level blocks (Devices → Block DNS) still apply while paused.

<a id="dashboard"></a>

## Dashboard

What your network did over a window you choose: queries, blocked, block rate, a volume chart, the most-blocked domains and the busiest devices — every number for the same window.

- Pick the window at the top (24 hours, 7, 30 or 90 days). It is remembered in this browser.
- Click any number, domain or device to open exactly those queries in the Query Log.
- Possible threats (in red): a device behaving like malware — many random-looking domains in a few minutes, long random names under one domain (data leaving through DNS), or lookups of known malicious domains. See lookups shows the evidence; This is fine hides it; Never flag adds a domain to the ignore list (Settings → Filtering).
- Something may be broken: when a blocklist starts blocking a name a device used to reach (answered normally at least 3 times in the week before) and the device keeps asking for it — 15 times in a couple of minutes, the way an app retries when it has stopped working — Needs attention names the device (a link to its page) and the domain, says how often it used to work, and offers Allow, Allow 1 h and Dismiss (quiet for a week). Names blocked all along, such as page tags and ad hosts, never trigger it however often they are asked for, and nor do blocks you chose yourself (your blocked domains, blocked services, group rules, a blocked device). It can also notify you; the notification opens the device. Lookups let through while blocking was paused show "paused" in the Query Log and do not count as having worked.
- Needs attention appears only when something does: no rules loaded, a list failing or retired, a resolver not answering, no password or API token, a nearly full disk, DNSSEC unable to check answers, or new warnings in Logs. Each line links to its fix.
- The line under the tiles gives totals since Minos last started; the cache hit rate is also since the last start.
- The Host card (not shown in containers) reports the machine Minos runs on; the disk figure is the filesystem holding the query log.
- The DNSSEC card appears when validation is on: in permissive mode it lists answers that enforce mode would have refused.

<a id="querylog"></a>

## Query Log

Every DNS lookup your devices make — streaming live, and searchable across the whole kept history — with the list and rule behind every block.

- With no filter the page is Live: new lookups arrive at the top. Pause holds the rows still while you read; new ones wait, counted.
- Any filter switches to Results: matches from the full history, newest first, with Load older.
- Filter with the result buttons (All, Blocked, Allowed, Would block), the list menu and the time range — or click any device, domain, type, list or resolver in a row.
- The search box takes filters as you type: blocked, allowed, would-block, client:192.168.1.5, domain:example.com (that exact name), type:AAAA, list:StevenBlack (quote names with spaces), upstream:1.1.1.1:853, dnssec:bogus, and -cdn to leave out names containing "cdn". Anything else matches domains, device addresses and device names — kitchen finds every query from the Kitchen iPad. Press / to search, Enter to apply, Esc to clear, Backspace to remove the last filter.
- Row actions: Allow (on blocked rows) or Block (on allowed rows), 1 h (allow for an hour, then block again), Check (how the domain would be judged now) and Copy.
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
- Is each list worth it? (shown with two or more blocklists switched on) compares them: how much of each the others already cover, how many lookups in the last 7, 30 or 90 days only that list would have blocked (with the busiest of them), and roughly how much memory removing it would free. A list that caught nothing the others would not have, and is mostly covered by another, is a candidate to remove. Audit-only lists are left out.

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
| Allowed for a limited time<br><small>Blocking → Allowed</small> | `blocking.temporary_allows` | — | Domains allowed until a time, then blocked again by themselves — "Allow for 1 hour" on a Query Log row or in Check a domain. Shown in the Query Log as allowed by temporary-allow. Up to a week; allowing a domain for good replaces its temporary allow.<br>`domain`: The domain, with subdomains.<br>`until`: When the allow ends (a timestamp, e.g. 2026-10-26T15:00:00Z). | at once |
| Blocked domains<br><small>Blocking → Blocked</small> | `blocking.denylist` | — | Always blocked, with subdomains. An entry written /pattern/ is a regex rule, e.g. /^ad[0-9]*\./ — checked after the exact rules, so an allowed domain still wins. Regexes use RE2 (no backreferences or lookaround); up to 1000 per list. | at once |
| Blocked services<br><small>Blocking → Allowed / Blocked → Services</small> | `blocking.services` | — | Services blocked for everyone. | at once |
| Allowed services<br><small>Blocking → Allowed / Blocked → Services</small> | `blocking.allowed_services` | — | Services allowed for everyone; allowing beats blocking. | at once |
| Your custom services<br><small>Blocking → Allowed / Blocked → Your custom services</small> | `blocking.custom_services` | — | Your own service bundles.<br>`name`: Its ID (derived from the label if not given).<br>`label`: Its display name.<br>`domains`: The domains it covers.<br>`allow_extra`: Extra hosts allowed only while the service is allowed.<br>`blocked`: Blocked for everyone.<br>`allowed`: Allowed for everyone. | at once |

<a id="privacy"></a>

## Privacy

Which tracking companies your devices talked to — advertising, analytics and telemetry — what got through and what your blocklists stopped, for the whole network or one device (on its page).

- Each company shows how many of its tracking lookups got through and how many were blocked; the bar is the blocked share. Open it for its names, what each is for, and (for the whole network) which devices looked it up.
- A name counts as tracking only when it is one of the company's advertising, analytics or telemetry names that a curated DNS blocklist (Hagezi Pro) also blocks. A company's own sites and services — Google search, YouTube, Facebook itself, time servers — never count, and are listed separately as other companies.
- Block (company)'s tracking blocks that company's tracking names for every device, as the list "company:<id>" in the Query Log. Its own sites, apps and sign-in keep working. Stop blocking undoes it; it is also in Settings → History.
- Periods are whole days (UTC): today, the last 7 days or the last 30 days.
- Company data: AdGuard companiesdb (built from WhoTracks.me), CC BY-SA 4.0. Tracking names: Hagezi DNS blocklists, GPLv3. Both are built into Minos and updated with releases; nothing is sent anywhere.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Companies whose tracking is blocked<br><small>Privacy</small> | `blocking.companies` | none | Company IDs (from the Privacy page) whose advertising, analytics and telemetry names are blocked for everyone. | at once |

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

<a id="home-assistant"></a>

## Home Assistant

Minos can appear in Home Assistant as a device of its own — a blocking switch, pause buttons, counters, problem sensors and a Block switch for each named device — with no YAML. It talks to Home Assistant through MQTT, the messaging system Home Assistant uses for devices like this.

- In Home Assistant, go to Settings → Add-ons, install the Mosquitto broker add-on and start it. Home Assistant then offers the MQTT integration under Settings → Devices & services; add it.
- Still in Home Assistant, make a user for Minos (Settings → People → Users → Add person, with a login) — for example "minos" with its own password. The Mosquitto add-on lets any Home Assistant user log in to the broker.
- In Minos, open Settings → Home Assistant. Enter the broker as mqtt://<Home Assistant's address>:1883 (for example mqtt://192.168.1.10:1883), the username and password you just made, tick Connect to Home Assistant and save. The status line turns green with "Connected to the broker".
- In Home Assistant, open Settings → Devices & services → MQTT → Devices: Minos is listed. Add its entities to a dashboard like any other device.
- Entities: switch.minos_blocking (off pauses blocking until turned back on); button.minos_pause_blocking_for_5_minutes and …_for_1_hour; sensor.minos_queries, sensor.minos_blocked_queries and sensor.minos_share_blocked (since Minos last started); sensor.minos_blocklist_rules; binary_sensor.minos_upstream_resolver_problem, binary_sensor.minos_threat_suspected and binary_sensor.minos_blocking_may_have_broken_something (on when there is a problem); update.minos_update (when Check for new releases is on); and switch.minos_block_<device> for each device you have named on the Devices page.
- State is sent when it changes and every 30 seconds. If Minos stops, its entities show as unavailable; if you untick Connect to Home Assistant, they are removed.
- Anything that can publish MQTT can control Minos too: minos/blocking/set (ON or OFF), minos/pause/set (minutes, e.g. 30) and minos/device/<id>/set (ON blocks, OFF unblocks). Do not send them retained — a retained command is ignored. Minos publishes minos/state (JSON), minos/availability (online/offline) and minos/device/<id>/state.
- No MQTT broker? Home Assistant's REST integrations work against the Minos API instead: set an API token (Settings → Access & security) and send it as the X-Api-Token header. docs/home-assistant.md has ready-made REST sensors, a switch and commands.

**Bedtime: the kids' tablet goes offline at 21:00 and back at 07:00**

```yaml
automation:
  - alias: Kids tablet off at bedtime
    triggers:
      - trigger: time
        at: "21:00:00"
    actions:
      - action: switch.turn_on
        target:
          entity_id: switch.minos_block_kids_tablet
  - alias: Kids tablet back in the morning
    triggers:
      - trigger: time
        at: "07:00:00"
    actions:
      - action: switch.turn_off
        target:
          entity_id: switch.minos_block_kids_tablet
```

**Tell me on my phone when Minos suspects a threat**

```yaml
automation:
  - alias: Minos threat
    triggers:
      - trigger: state
        entity_id: binary_sensor.minos_threat_suspected
        to: "on"
    actions:
      - action: notify.mobile_app_your_phone
        data:
          title: Minos suspects a threat
          message: Open Minos to see which device.
```

**Pause blocking for 15 minutes from a script or another automation**

```yaml
script:
  minos_pause_15:
    alias: Pause Minos for 15 minutes
    sequence:
      - action: mqtt.publish
        data:
          topic: minos/pause/set
          payload: "15"
```

**The status says "not authorized" or "wrong username or password".** The broker refused the login. Check the Home Assistant user and its password (log in to Home Assistant with it once to be sure), then type the password again in Minos — it is never shown, so retyping is the way to be sure it is right.

**The status says it cannot reach the broker.** Check the address and port (1883, or 8883 for mqtts://), that the Mosquitto add-on is running, and that Home Assistant's machine accepts connections on that port from the machine Minos runs on.

**Minos says connected, but no Minos device appears in Home Assistant.** The MQTT integration must use the same broker, and discovery must be on with the prefix "homeassistant" (Settings → Devices & services → MQTT → Configure). If you changed the prefix there, set the same Discovery prefix in Minos.

**A device has no Block switch.** Switches are made for devices with a name: give the device one on the Devices page (or its own page) and the switch appears within half a minute. Add a Block switch for each named device must also be ticked.

**I run two Minos servers on one broker.** Give each its own Topic prefix (minos and minos2, say) in Settings → Home Assistant, or their entities collide.

**How do I take Minos out of Home Assistant?** Untick Connect to Home Assistant and save; Minos removes its entities before it disconnects.

<a id="settings-dns"></a>

## Settings → DNS

Where Minos looks up allowed names, whether it checks DNSSEC, and its answer cache.

- Upstream resolvers are tried in order: the first is the primary. The light beside each is green when it is answering, red when failing, hollow when it has not been needed yet.
- Encrypted protocols (DoH, DoT) keep your lookups private from your internet provider; the presets are verified to work.
- Changes on every Settings tab are drafts until you press Save changes in the bar at the bottom.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Upstream resolvers<br><small>Settings → DNS</small> | `dns.upstreams` | Cloudflare: https://1.1.1.1/dns-query (DoH), then 1.0.0.1:853 (DoT) | Where allowed lookups are sent, in order. A resolver that fails three times in a row is skipped, and Minos checks it again every 30 seconds until it answers — the light turns green by itself. Backups are also checked every 30 minutes, so a dead one shows before the day it is needed. Test asks a resolver a question now, saved or not.<br>`address`: host:port for udp, tcp, dot and doq; an https:// URL for doh and doh3.<br>`protocol`: udp, tcp, dot (DNS over TLS), doh (DNS over HTTPS), doh3 (DNS over HTTPS on HTTP/3) or doq (DNS over QUIC). | at once |
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
| Threat detection<br><small>Settings → Filtering</small> | `threats.enabled` | on | Watches for devices behaving like malware and says so under Needs attention (and in a notification): many random-looking domains in a few minutes (how malware finds its control server), long random names under one domain (data sent out through DNS), or a lookup blocked by a threat-intelligence list such as URLhaus or Hagezi TIF. "This is fine" hides a finding for a week (a day for known malicious domains). It reads the query log in the background, so lookups are never slowed. Tuned on 90 days of a real home network: no false alarms for the first two kinds. | at once |
| Never flag these domains<br><small>Settings → Filtering</small> | `threats.ignore` | none | Domains (with subdomains) threat detection leaves alone — a service that legitimately uses long random names. Antivirus and spam-reputation lookups (Sophos, McAfee, ESET, Spamhaus…) are already left out. "Never flag" on a finding adds to it. | at once |

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

<a id="settings-homeassistant"></a>

## Settings → Home Assistant

Puts Minos in Home Assistant with no YAML. Minos announces itself over MQTT (Home Assistant's "MQTT discovery"), so a Minos device appears with everything below, kept up to date within seconds. The Home Assistant section of Help has the step-by-step setup, the entity names and example automations.

- In Home Assistant, install the Mosquitto broker add-on (Settings → Add-ons) and add the MQTT integration if it isn't there already.
- Make a Home Assistant user for Minos (Settings → People → Users), for example "minos" — the Mosquitto add-on accepts Home Assistant users.
- Here, enter the broker as mqtt://<Home Assistant's address>:1883 with that username and password, tick Connect to Home Assistant and save. The status line says when Minos is connected.
- In Home Assistant, open Settings → Devices & services → MQTT: the Minos device is listed with its entities.
- What you get: a Blocking switch (off pauses blocking until you turn it back on), Pause blocking for 5 minutes and for 1 hour buttons, Queries, Blocked queries and Share blocked sensors (counted since Minos started), Blocklist rules, problem sensors for an upstream resolver failing, a suspected threat and blocking that may have broken something, an Update entity when Check for new releases is on, and a Block switch for each device you have named.
- Changes made from Home Assistant act straight away; blocking a device is recorded in Settings → History as "Home Assistant (MQTT)".

**The status says "wrong username or password" or "not authorized".** Check the user in Home Assistant (or the broker's password file). With the Mosquitto add-on, a Home Assistant user works; an "owner" account works too but a separate user is safer.

**Minos is connected but nothing appears in Home Assistant.** The MQTT integration in Home Assistant must be using the same broker, and its discovery prefix must match (homeassistant unless you changed it).

**How do I remove Minos from Home Assistant?** Untick Connect to Home Assistant and save: Minos removes its entities before it disconnects.

| Setting | Config key | Default | What it does | Applies |
|---|---|---|---|---|
| Connect to Home Assistant<br><small>Settings → Home Assistant</small> | `home_assistant.enabled` | off | Connects to the MQTT broker and announces Minos to Home Assistant. Turning it off removes the Minos entities from Home Assistant. | at once |
| MQTT broker<br><small>Settings → Home Assistant</small> | `home_assistant.broker` | none | The broker Home Assistant uses: mqtt://host:1883, or mqtts://host:8883 for TLS (the certificate must be valid for the host name). If the broker goes away Minos keeps trying, waiting up to a minute between attempts. | at once |
| Username<br><small>Settings → Home Assistant</small> | `home_assistant.username` | none | The broker login. For the Mosquitto add-on, a Home Assistant user. | at once |
| Password<br><small>Settings → Home Assistant</small> | `home_assistant.password` | none | The broker password. Stored in the config file; never shown again, and hidden in the settings history. | at once |
| Add a Block switch for each named device<br><small>Settings → Home Assistant</small> | `home_assistant.device_switches` | on | One switch per device you have named on the Devices page. On blocks all of that device's DNS, exactly like Block device in Minos — handy for a bedtime automation. | at once |
| Discovery prefix<br><small>Settings → Home Assistant</small> | `home_assistant.discovery_prefix` | homeassistant | Where Home Assistant listens for announcements. Change it only if you changed it in Home Assistant's MQTT settings. | at once |
| Topic prefix<br><small>Settings → Home Assistant</small> | `home_assistant.topic_prefix` | minos | Where Minos publishes its state and listens for commands (minos/state, minos/blocking/set…). Give each Minos its own when two share a broker. | at once |

<a id="settings-access"></a>

## Settings → Access & security

Who can use this interface and the API, and which devices may use Minos for DNS.

**I forgot the password.** On the Minos machine, delete the password_hash line (and totp_secret, if you set up an authenticator app) from minos.yaml, then restart Minos. The interface is open again until you set a new password.

**I lost the phone with my authenticator app.** Delete totp_secret from minos.yaml and restart Minos; signing in then asks only for the password. Set the app up again afterwards.

**Home Assistant or a script stopped working after I set a password.** Scripts cannot sign in with a password. Set an API token here as well and send it as X-Api-Token (or Authorization: Bearer); both work side by side.

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
| Check for new releases once a day<br><small>Settings → System</small> | `update_check` | off | Asks GitHub for the latest release — nothing about you or your network is sent. A banner shows how to upgrade. On Linux installs made with the install script, and once a password or API token is set, it has an Update now button: Minos downloads the release, checks it is signed by the Minos release key and is newer, installs it and restarts. If the new version does not answer within 30 seconds, the previous one is put back and you are told. | at once |
| Install method<br><small>minos.yaml</small> | `update_install_method` | detected | binary, docker or source — forces the upgrade instructions when detection cannot tell. | after restart (minos.yaml) |

<a id="settings-backup"></a>

## Settings → Backup & import

Download the whole configuration (minos.yaml) as a backup, restore one, or bring your setup across from Pi-hole or AdGuard Home. Imports only add; nothing already configured is removed.

<a id="settings-history"></a>

## Settings → History

Every settings change made here, by a script or with the minos command — when, who (password sign-in, API token, the minos command, with the address) and which settings — with a way back. The last 100 changes are kept, beside the config file.

- Show changes opens the change itself, line by line. Passwords, tokens and keys are always hidden.
- Undo last change restores the version before the latest change; Restore this version restores any earlier one.
- Restoring never touches the password, authenticator codes, API token, notification and certificate secrets, list request headers, or settings that need a restart (listen addresses, encrypted DNS, query-log storage) — so it cannot lock you out. A restore is recorded as a change of its own, so it can be undone too.
- Minos's own housekeeping (an hour's allow ending, say) is not recorded.

<a id="cli"></a>

## Command line

The minos command talks to the running server through its API, using -config to find the address and credentials — the API token, or with only a password set, a sign-in it makes from the config file itself.

- minos status — counters, rules and pause state.
- minos pause 30m / minos resume — pause and resume blocking.
- minos logs -f -level warn — follow the application log.
- minos import pihole /etc/pihole or minos import adguard AdGuardHome.yaml — migrate settings.
- minos version.
