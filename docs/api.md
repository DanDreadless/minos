# REST API reference

Everything the web UI does goes through this API, so everything the UI can
do, your scripts can do. The base URL is the API listener (default
`http://<host>:8080`). All field names are plain and literal — the themed
labels live only in the UI.

## Authentication

When `api.token` is set, every endpoint requires it. Send either header:

```sh
curl -H "X-Api-Token: $TOKEN"        http://minos:8080/api/status
curl -H "Authorization: Bearer $TOKEN" http://minos:8080/api/status
```

The WebSocket stream also accepts `?token=` (browsers cannot set headers on
WebSockets); no other endpoint reads the query parameter.

## Status & statistics

### `GET /api/status`

Counters and state, cheap enough to poll:

```json
{
  "version": "0.6.0",
  "uptime_seconds": 86400,
  "paused": false,
  "queries_total": 48210,
  "queries_blocked": 9114,
  "entries_dropped": 0,
  "rules": 82937,
  "allow_rules": 12,
  "rules_skipped": 0,
  "cache_enabled": true,
  "cache_hits": 21050,
  "cache_misses": 18046,
  "cache_entries": 3120,
  "update_available": false,
  "dnssec": {"mode": "permissive", "secure": 4120, "insecure": 31882,
             "bogus": 7, "indeterminate": 44}
}
```

`paused_until` (RFC 3339) appears during a timed pause; `latest_version`
appears once the opt-in update check has run.

`dnssec` is **absent entirely** when `dns.dnssec` is off (or the proxy is
not wired), so clients should treat absence as "validation disabled"
rather than looking for a separate flag. `mode` is `permissive` or
`enforce`. The four counters are **process-lifetime** and reset on
restart — for a windowed figure see `GET /api/stats`.
### `GET /api/host`

The machine Minos runs on, for the dashboard's Host card.

```json
{
  "supported": true,
  "hostname": "vault-tec", "os": "linux", "arch": "arm64", "cpus": 4,
  "kernel": "6.6.51+rpt-rpi-v8", "platform": "Debian GNU/Linux 12 (bookworm)",
  "go_version": "go1.26.4", "container": false,
  "mem_total": 4127195136, "mem_source": "host",
  "sample": {
    "time": "2026-08-27T14:31:02Z",
    "cpu_percent": 3.9, "load1": 0.21, "load5": 0.18, "load15": 0.14,
    "mem_used": 512483328, "mem_available": 3614711808,
    "disk_total": 31427035136, "disk_free": 24903090176,
    "temp_celsius": 48.7, "uptime_seconds": 934812,
    "proc_rss": 94371840, "goroutines": 38
  }
}
```

**On a container install the entire response is `{"supported": false}`.**
Not a 404 — the endpoint exists and answered; the feature does not apply
there. Clients must branch on `supported` rather than reading missing
fields as zeroes. See the Host health section of the getting-started
guide for why containers report nothing.

**Every field inside `sample` is optional except `time` and
`goroutines`.** A platform that cannot measure a reading omits it:
Windows has no `load*`, macOS no `cpu_percent` or memory breakdown, and
`temp_celsius` needs a hardware sensor. Render an absent field as
unknown, never as zero.

`mem_source` is `host` or `cgroup`. When it is `cgroup`, `mem_total` and
the sample's memory figures describe the limit Minos was given rather
than the machine — do not present them as the host's RAM.

`disk_total`/`disk_free` are for the filesystem holding the query log,
not the largest one. `uptime_seconds` is the host's; the process's own
uptime is on `/api/status`.

The values come from a sampler on its own ticker (10s), so this endpoint
never blocks. `cpu_percent` is a delta between two readings and is
therefore absent from the very first sample after start.

### `GET /api/update`

The running and latest versions plus an actionable upgrade command for how
*this* instance was installed:

```json
{"current": "0.7.0", "latest": "0.8.0", "available": true,
 "install_method": "binary",
 "command": "curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh && sudo systemctl restart minos",
 "notes_url": "https://github.com/DanDreadless/minos/releases/tag/v0.8.0"}
```

`install_method` is `binary` (quick-install), `docker`, or `source`,
decided in this order: the `update_install_method` config override if set,
then runtime container markers, then the build-time stamp release and
Docker builds carry, then a dev-version heuristic. Display-only — Minos
never runs the command itself.

### `GET /api/stats?hours=24`

Dashboard aggregates for a 1–2160 hour window (up to 90 days): a
`timeline` of `{time, total, blocked}` buckets (10-minute buckets up to
24 h, hourly to 7 days, daily beyond), `top_blocked` as `{qname, count}`,
and `top_clients` as `{client, total, blocked}`. Entries not yet flushed
to disk (up to 30 s) are not included.

While `dns.dnssec` is on, the response also carries `dnssec`:

```json
{"dnssec": {"would_block": 12,
            "top_domains": [{"qname": "dnssec-failed.org", "count": 9}]}}
```

`would_block` counts answers **in this window** that failed validation
and were let through — what enforce mode would have refused. It is read
from the `audit_list = "dnssec"` marks in the query log, so it is
windowed, unlike the process-lifetime counters on `/api/status`. In
steady-state `enforce` it is zero: refusals are blocks, and appear under
`list` instead (see `GET /api/stats/lists`). The block is reported
whenever validation is on rather than only in permissive mode, because
the mode swaps live and a window can span a change.

Same sampling caveat as audit lists: validation runs when an answer is
fetched from upstream, so cache hits are not re-validated and add no
rows. `would_block` is therefore lower than the `bogus` counter on
`/api/status` — they count resolutions and lookups respectively.

### `GET /api/stats/client?client=192.168.1.50,192.168.1.51&hours=24`

One device's traffic, aggregated for the device page's activity section.
`client` is required: exact addresses, comma-separated because a device can
span several IPs across DHCP leases (pass everything from the device's
`ips[]`). `hours` is 1–2160 (default 24) — 2160 covers the default 90-day
retention.

```json
{"window_hours": 24, "total": 1042, "blocked": 87,
 "top_allowed": [{"qname": "github.com", "count": 120}],
 "top_blocked": [{"qname": "ads.example.com", "count": 33}]}
```

Like every aggregate, it reads the persisted log (or the ring in ephemeral
mode), so entries not yet flushed (up to 30 s) are missing.

### `GET /api/stats/lists?hours=168`

Blocks attributed to each list, busiest first — which lists earn their
keep. `hours` is 1–2160 (default 168, a 7-day week). Names are whatever the
docket's `list` field carries: subscribed list names plus the built-in
pseudo-lists (`denylist`, `service:<name>`, `group:<name>`, `clients`).

```json
{"window_hours": 168, "lists": [
  {"list": "StevenBlack", "count": 1893},
  {"list": "service:youtube", "count": 240}]}
```

### `GET /api/check?domain=ads.example.com`

Judge a name without querying it:

```json
{"domain": "ads.example.com", "verdict": "blocked", "list": "StevenBlack", "rule": "ads.example.com"}
```

`verdict` is `blocked` or `allowed`; `list`/`rule` say which rule decided
(empty when no rule matched). The check ignores an active pause on purpose.

### `GET /api/upstreams`

Live failover-breaker state per upstream, keyed by the configured address —
the data behind the health lights next to each resolver in Settings:

```json
[{"address": "https://1.1.1.1/dns-query", "requests": 5120, "failures": 2,
  "avg_ms": 18.4, "sick": false}]
```

`sick` means the breaker is currently sidestepping the upstream (it is
retried every 30 s). `requests: 0` means the upstream has not been needed
since the last restart — normal for a backup behind healthy primaries, not
an outage. The Settings health light is two-state: red when `sick`, green
otherwise (a `requests: 0` backup is a healthy green, standing by); the
tooltip carries the active-vs-standby detail.

## Query log

### `GET /api/querylog?limit=100`

Newest first, `limit` 1–10000, from the in-memory ring:

```json
[{"time": "2026-07-05T10:12:03Z", "client": "192.168.1.50",
  "qname": "doubleclick.net", "qtype": "A", "verdict": "blocked",
  "list": "StevenBlack", "rule": "doubleclick.net", "duration_ms": 0.21}]
```

Allowed entries carry `upstream` instead of `list`/`rule` — the resolver
that answered, or `cache`, `stale`, `local`, or `safesearch`. An allowed
entry may also carry `audit_list`/`audit_rule`: an audit-mode list would
have blocked it ("would block" in the UI). Cached answers skip judgment,
so audit marks are sampled at resolution time, not on every hit.

`dnssec` carries the validation outcome — `secure`, `insecure`, `bogus` or
`indeterminate` — and is absent when validation is off, when nothing was
judged (a block, a local record, a route), or on a cache hit. Same
sampling rule as audit marks: it records resolutions, not lookups.

### `GET /api/querylog/history?q=&client=&verdict=&would_block=&list=&dnssec=&before=&limit=`

The persisted log (SQLite), newest first — the full retained history behind
search and the dashboard drill-downs, not just the in-memory ring. `q`
matches a client IP or domain substring; `client` is an **exact** address
filter, comma-separated for a device with several IPs (the Devices
drill-down), distinct from the `q` substring; `verdict` is
`blocked`/`allowed`/`all`; `would_block=true` narrows to entries an
audit-mode list flagged; `list` is an **exact** list-name filter matching
either the enforcing attribution (`list`) or the audit one (`audit_list`) —
so it finds what a list condemned, pardoned, or would have blocked;
`dnssec` narrows to one validation outcome (the Tribunal's counter
drill-down) and rejects anything outside the four with a 400, so a typo
reads as an error rather than as "no matches";
`before` is a unix-millis cursor for "load older"
pagination; `limit` 1–1000. Returns `[]` in ephemeral mode (there the ring
already backs both the log and the dashboard, so the UI filters it directly).

### `GET /api/querylog/lists?hours=168`

The distinct list names attributed in the window (enforcing and audit),
sorted — the options behind the Docket's list filter. `hours` 1–2160.
Ring-backed in ephemeral mode.

```json
["StevenBlack", "denylist", "service:netflix", "strict-audit"]
```

### `GET /api/querylog/stream` (WebSocket)

Pushes each entry as a JSON frame in the same shape, as it happens.

## Blocking control

### `POST /api/pause`

```sh
curl -X POST -d '{"duration": "30m"}' http://minos:8080/api/pause
```

Empty duration pauses until resumed. Response: `{"paused": true,
"paused_until": "..."}`. Note: recess does not lift device-level DNS
blocks — those are access control.

### `DELETE /api/pause`

Resumes blocking. Response: `{"paused": false}`.

## Allow & deny domains

`GET /api/allowlist` and `GET /api/denylist` return plain string arrays.
Add with `POST` (`{"domain": "example.com"}`); remove with
`DELETE /api/allowlist/{domain}`. Entries cover subdomains, and allow
always beats deny.

## Lists

- `GET /api/lists` — per-list status: `{name, url, format, action, enabled,
  audit, rules, skipped, last_refresh, last_error}`
- `POST /api/lists` — add: `{"name", "url", "format": "hosts|plain|adblock",
  "action": "block|allow", "audit": false, "enabled": true}` (fetches
  immediately). `audit: true` puts a blocklist in **audit mode**: its rules
  compile into a separate matcher that is consulted but never enforced —
  matching queries are forwarded normally and logged with
  `audit_list`/`audit_rule` ("would block"). Try a strict list safely, then
  flip `audit` off to enforce it; auditing an `allow` list is rejected.
  `action` defaults to `block`; `allow` makes the source a subscribed allowlist —
  every entry is always allowed, beating any blocklist, and a passing
  verdict names it in the query log. In an `allow` list, block-shaped
  AdBlock rules count as allows too (membership decides meaning, matching
  AdGuard whitelist filters and Pi-hole v6 antigravity lists). In the
  config file, allowlists live under `lists.allow_sources` (block lists
  under `lists.sources`); names are unique across both.
- `PUT /api/lists/{name}` — change any of `url`, `format`, `action`,
  `audit`, `enabled`; changing `action` moves the list between `sources` and
  `allow_sources`
- `DELETE /api/lists/{name}`
- `POST /api/lists/refresh` — refetch everything now (synchronous)

## Services

- `GET /api/services` — `{"catalog": [{name, label, domains}],
  "blocked": [...], "allowed": [...]}`. `blocked` services are denied for
  everyone; `allowed` services are pardoned for everyone — every domain the
  service needs, including playback/sign-in hosts the deny bundle doesn't
  carry. Allow beats deny, so a service that is both ends up allowed.
- `PUT /api/services` — partial update; each present field replaces its
  set, omitted fields stay untouched: `{"blocked": ["tiktok"]}`,
  `{"allowed": ["netflix"]}`, or both.

### Custom services

User-defined bundles that behave like catalog services everywhere —
`GET /api/services` returns them as `"custom": [{name, label, domains,
allow_extra, blocked, allowed}]`. Unlike catalog services, a custom's
**global** block/pardon toggles live on the definition itself (`blocked`/
`allowed`), never inside the `blocked`/`allowed` arrays above — those are
validated against the compiled-in catalog, and keeping custom names out of
them is what lets an older binary still boot this config (it drops the
unknown `custom_services` keys whole).

- `POST /api/services/custom` — `{"label", "domains": [...], "name"?,
  "allow_extra"?, "blocked"?, "allowed"?}`. `name` (a lowercase slug, max
  40 chars) is derived from the label when omitted; a clash with a
  catalog service name is rejected. `allow_extra` lists hosts pardoned
  only when the service is allowed (sign-in/CDN hosts), like the
  catalog's curated extras.
- `PUT /api/services/custom/{name}` — partial update of any of `label`,
  `domains`, `allow_extra`, `blocked`, `allowed`. The name is the stable
  key; renaming is not supported.
- `DELETE /api/services/custom/{name}` — removes the definition and
  clears it from every group's custom selections.

Matches attribute to `service:<name>` in the query log, same as catalog
services.

## Devices & groups

- `GET /api/clients` — every device that has queried plus every configured
  one, **one row per physical device**: `{ip, ips, mac, vendor, hostname,
  name, notes, group, blocked, seen, queries, queries_blocked, first_seen,
  last_seen}`. A device is identified by its MAC when known, so all the IPs
  it has held across DHCP leases fold into one entry — `ip` is the primary
  (most recently active) address and `ips` lists them all (used by the Docket
  drill-down); counts are summed across them. `vendor` is derived from the
  MAC via the full IEEE registry (MA-L/MA-M/MA-S/IAB, longest prefix wins);
  `private_mac: true` marks a randomized locally-administered MAC that no
  registry can name. `hostname` comes from reverse DNS via the gateway,
  falling back to NetBIOS then mDNS `.local` — all best-effort — and
  `name_source` says which source it was (`ptr`, `netbios`, `mdns`, `ssdp`,
  `dhcp`; a stronger source is never overwritten by a weaker one). `model`
  carries a device's own self-description when a source offered one, and a
  self-reported manufacturer overrides the registry-derived `vendor`.
  `hint` is a coarse OS/type guess (DHCP fingerprint or traffic patterns)
  for devices nothing else names — presented in the UI as a guess, never a
  fact, and outranked by every real source.
- `PUT /api/clients/{key}` — upsert any of `{"name", "mac", "group",
  "blocked", "notes"}` (`"group": "default"` unassigns; `notes` is
  free-form user text up to 4096 chars, persisted with the assignment and
  shown on the device page). `{key}` is the device's **MAC**
  when it has one (so the assignment follows it across DHCP leases) or its
  **IP** otherwise; a MAC key resolves the device's current IP automatically,
  or accepts an `"ip"` field as a last-known-address hint when it's offline.
- `DELETE /api/clients/{key}` — forget the saved assignment (`{key}` is the
  MAC or IP, as above)
- `GET /api/groups` / `POST /api/groups` — groups are `{name, mode:
  "filter|bypass|block", allowlist, denylist, services, allowed_services,
  custom_services, allowed_custom_services, safe_search, schedule}`;
  `services` are blocked and `allowed_services` pardoned for members only;
  `custom_services`/`allowed_custom_services` do the same for user-defined
  custom services (separate fields on purpose — custom names never enter
  the catalog-validated keys); `schedule` is `{days: ["mon", ...], start:
  "21:00", end: "07:00"}` or `null` to clear
- `PUT /api/groups/{name}` / `DELETE /api/groups/{name}`

## Settings

- `GET /api/config` — the current config with secrets redacted to
  `*_set` booleans
- `PUT /api/config` — partial update; omitted fields stay untouched. All
  runtime settings apply immediately. The listen addresses, `dns.tls`, and
  query-log storage are file-only and not writable here. See
  [getting-started.md](getting-started.md) for the full field reference.
- `GET /api/config/export` — the live config as downloadable YAML
  (includes secrets; it is a backup)
- `POST /api/config/import` — restore a config from an uploaded YAML body
  (the export above). The whole config is replaced, except the file-only
  listen addresses, `dns.tls`, and query-log storage, which are kept from
  the running instance. Returns the resulting config view.

## Import from Pi-hole / AdGuard Home

Append-only uploads (multipart form, 64 MB cap) — existing settings are
never removed, duplicates are dropped, and the response reports what was
added plus a `skipped` list of anything that couldn't map:

- `POST /api/import/pihole` — form fields `gravity` (a `gravity.db`,
  required) and `custom_list` (a `custom.list`, optional)
- `POST /api/import/adguard` — form field `config` (an `AdGuardHome.yaml`)

```json
{"lists": 3, "allow": 2, "deny": 41, "local_records": 5, "services": 0,
 "skipped": ["regex rule \"^ads\\.\": Minos does not support regex rules"]}
```

## Monitoring

`GET /metrics` serves Prometheus exposition format — see the
[monitoring section](getting-started.md#monitoring-with-prometheus--grafana)
and the ready-made dashboard in `deploy/grafana-dashboard.json`.

## Notifications (outbound)

Configured via `notifications` in the config or Settings; each event is
POSTed to your webhook as:

```json
{"type": "device_new | upstream_sick | upstream_recovered | update_available | digest",
 "title": "New device on your network",
 "message": "192.168.1.77 (phone.lan) [aa:bb:cc:dd:ee:ff] made its first DNS query through Minos.",
 "time": "2026-07-05T10:12:03Z"}
```

`digest` events are the opt-in traffic summary (`notifications.digest:
daily|weekly`): totals, block rate, top blocked domains, busiest client,
and new-device count for the period, as a plain-text `message`.

## Errors

Non-2xx responses carry `{"error": "plain description"}` — 400 for invalid
input (validation failures apply nothing), 401 for a missing/wrong token,
404 for unknown names/IPs.
