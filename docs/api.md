# REST API reference

Everything the web UI does goes through this API, so everything the UI can
do, your scripts can do. The base URL is the API listener (default
`http://<host>:8080`). All field names are plain and literal.

## Authentication

With neither a password nor `api.token` set, the API is open to your
network. Once either is set, every endpoint below needs credentials, except
`GET /api/auth` and the sign-in calls.

**API token** — for scripts and automation. Send either header:

```sh
curl -H "X-Api-Token: $TOKEN"        http://minos:8080/api/status
curl -H "Authorization: Bearer $TOKEN" http://minos:8080/api/status
```

The WebSocket streams also accept `?token=` (browsers cannot set headers on
WebSockets); no other endpoint reads the query parameter.

**Password** — for people. `POST /api/auth/login` with
`{"password": "...", "code": "123456"}` sets the `minos_session` cookie
(HttpOnly, SameSite=Strict, 30 days). A cookie-authenticated request must
also come from the same origin, so another site cannot ride the cookie.
Five failed attempts from one address in a minute answer `429` with
`Retry-After`.

| Endpoint | |
|---|---|
| `GET /api/auth` | `{"password_set", "totp_enabled", "token_set", "authenticated"}` — no credentials needed |
| `POST /api/auth/login` | Sign in. `401` with `"need_code": true` when an authenticator code is required or wrong |
| `POST /api/auth/logout` | Clear this browser's session |
| `PUT /api/auth/password` | `{"current", "new"}` — set or change the password (8+ characters); ends every other session |
| `DELETE /api/auth/password` | `{"current"}` — remove the password (and authenticator codes) |
| `POST /api/auth/signout-all` | End every session except this one |
| `POST /api/auth/totp/setup` | Start authenticator setup: `{"secret", "uri"}` (the `otpauth://` link) |
| `POST /api/auth/totp/enable` | `{"code"}` — confirm a code from the app to turn it on |
| `DELETE /api/auth/totp` | `{"current"}` — turn authenticator codes off |

Neither the password hash nor any secret is ever returned; settings report
only `password_set` and `totp_enabled`.

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
Docker builds carry, then a dev-version heuristic. The command is
display-only.

`self_update` is true when the one-click update below works on this
install: a Linux install made with the install script (which adds the
updater), running under systemd, with a password or API token set. When it
is false, `self_update_blocked` says why in plain words.

### `POST /api/update/install`

Installs the latest known release (`202 Accepted`). Minos never has root:
it writes the tag to `update-request` in its state directory, and
`minos-update.path` starts the root helper
(`/usr/local/libexec/minos-update`), which downloads the release, has the
installed binary verify its Ed25519 signature, checksum and that it is
newer, keeps the old binary as `minos.prev`, installs and restarts — and
restores `minos.prev` if the new version does not answer `minos status`
within 30 seconds. `409` when `self_update` is false, no newer release is
known, or an update is already running.

### `GET /api/update/progress`

The updater's last report, e.g.
`{"state": "verifying", "version": "v0.31.0", "from": "0.30.0", "message": "Checking the signature.", "time": 1792800131}`.
`state` is `requested`, `downloading`, `verifying`, `installing`,
`restarting`, `done`, `failed` or `rolled_back`; `{"state": ""}` when there
has been none. A working state older than 15 minutes reads as `failed`. A
finished update is also announced as a notification (type
`update_available`).

### `GET /api/stats?hours=24`

Dashboard aggregates for a 1–2160 hour window (up to 90 days): a
`timeline` of `{time, total, blocked}` buckets (10-minute buckets up to
24 h, hourly to 7 days, daily beyond), `top_blocked` as `{qname, count}`,
and `top_clients` as `{client, total, blocked}`; `window_start` is when
the counted window begins. Entries not yet flushed to disk (up to 30 s) are
not included.

The aggregates are answered from rollup tables Minos maintains as it logs
(per 10 minutes, hour and day), plus the raw log for the partial hour at the
front of the window, so the answer is exact and the cost no longer grows
with the window: on a Raspberry Pi holding 13 million queries, 90 days takes
about as long as 24 hours. Right after upgrading onto a large existing log,
Minos fills the rollups in the background (minutes on a Pi); until then
these endpoints read the raw log and a very wide window can still run into
the 20-second cap, answering `503` with a message naming the window. Reads
use a separate SQLite connection pool from the writer, so a slow aggregate
never stalls query logging or the rest of the API.

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
{"window_hours": 24, "window_start": "2026-10-22T14:03:11Z",
 "total": 1042, "blocked": 87,
 "top_allowed": [{"qname": "github.com", "count": 120}],
 "top_blocked": [{"qname": "ads.example.com", "count": 33}]}
```

For windows of 48 hours and more the figures come from daily rollups and
cover whole UTC days, so `window_start` is the start of the first day
counted — label the window from it rather than from `hours`.

Like every aggregate, it reads the persisted log (or the ring in ephemeral
mode), so entries not yet flushed (up to 30 s) are missing.

### `GET /api/stats/lists?hours=168`

Blocks attributed to each list, busiest first — which lists earn their
keep. `hours` is 1–2160 (default 168, a 7-day week). Names are whatever the
query log's `list` field carries: subscribed list names plus the built-in
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

Live health per upstream, keyed by the configured address — the data behind
the health lights next to each resolver in Settings:

```json
[{"address": "https://1.1.1.1/dns-query", "requests": 5120, "failures": 2,
  "avg_ms": 18.4, "sick": false, "state": "healthy",
  "last_success": "2026-10-23T12:01:44Z", "last_failure": "2026-10-23T09:12:03Z",
  "last_error": "doh request: context deadline exceeded"}]
```

`state` is the one verdict to show:

- `healthy` — has answered, and is not currently failing.
- `failing` — the breaker tripped and nothing has succeeded since, **or**
  the upstream has never answered a single query. Minos probes a failing
  upstream itself every 30 s (a `. NS` question), so a backup that only
  queries never reach still turns healthy when it recovers. `sick` is the
  breaker half of that and stays true for the whole outage.
- `unused` — not asked anything yet. Minos probes every upstream shortly
  after it starts, and any no query has reached for 30 minutes, so this is
  brief.

Probes count as requests in these counters and in `/metrics`.

### Settings history

`GET /api/settings/history` lists recorded settings changes, newest first:
`[{"id", "time", "actor": "Password sign-in from 192.168.1.20", "note", "keys": ["blocking.allowlist"]}]`.
Changes made through the API are recorded (the last 100) with how the
request was authorized and its address — `Password sign-in`, `API token`,
`Open access`, or `minos command` for the CLI (which sends
`X-Minos-Client: cli`); Minos's own housekeeping is not.
`GET /api/settings/history/{id}/diff` compares that version with the one
before it as `[{"op": " "|"+"|"-", "text"}]` lines, secrets shown as
`(hidden)`. `POST /api/settings/history/{id}/restore` makes it current
again — except sign-in and other secrets and restart-only settings, which
stay as they are — and records the restore as a change of its own.

### `GET /api/threats`

Devices behaving like malware (threat detection, `threats.enabled`):

- `dga` — 12+ distinct random-looking registrable domains in 10 minutes;
- `tunnel` — 20+ distinct names under one domain in 5 minutes, each carrying
  50+ characters of long, high-entropy labels;
- `known_threat` — lookups blocked by a threat-intelligence list (name or URL
  mentions tif, threat, urlhaus, malware, phish, scam or ransom), one
  accumulating finding per device (`distinct` domains, `count` lookups).

```json
[{"kind": "dga", "client": "192.168.1.25", "device": "Doorbell", "count": 14,
  "samples": ["kqlxtqptsmys.com", "pxhbfogkrmplf.com"],
  "first": "2026-10-26T14:02:11Z", "last": "2026-10-26T14:09:40Z"}]
```

A finding lasts 24 hours after its last evidence. New findings are sent as
notifications of type `threat_suspected` at high priority.

`POST /api/threats/dismiss` with `{"kind", "client", "domain"}` ("This is
fine") hides one for a week (a day for `known_threat`; pass `"domain": ""`
for `dga` and `known_threat`). `POST /api/threats/ignore` with `{"domain"}`
adds it to `threats.ignore` — never flagged again, on any device.

### `GET /api/suggestions`

Apps that look broken by blocking: a device that asked for a name a
blocklist blocks at least 15 times within two minutes, when it had not been
doing so through the previous 24 hours (chronic tracker chatter is left out).
Only blocks from subscribed blocklists, CNAME cloaking and rebinding
protection count — never your own blocked domains, blocked services, group
rules or a blocked device. A suggestion lasts an hour after its last blocked
lookup and disappears as soon as the name is allowed.

```json
[{"client": "192.168.1.20", "device": "Living room TV", "domain": "api.app.example",
  "list": "StevenBlack", "rule": "api.app.example", "count": 42,
  "since": "2026-10-26T14:02:11Z", "last": "2026-10-26T14:03:40Z"}]
```

### `POST /api/suggestions/dismiss`

`{"client": "192.168.1.20", "domain": "api.app.example"}` hides that
suggestion and keeps the pair quiet for 24 hours. A new suggestion is also
sent as a notification of type `breakage_suspected` (mutable like the rest).

### Temporary allows

`POST /api/allowlist` with `{"domain": "ads.example.com", "minutes": 60}`
allows a domain (and its subdomains) until then, after which it is blocked
again by itself (`201` with `until`). Up to a week; a domain only — not a
`/regex/` rule. Allowing the domain again replaces the entry; allowing it
for good (no `minutes`) removes the temporary entry. Queries it passes are
attributed to the list `temporary-allow`.
`GET /api/allowlist/temporary` lists the active ones
(`[{"domain", "until"}]`); `DELETE /api/allowlist/temporary?domain=` ends one now.

### `POST /api/upstreams/test`

Asks one upstream a test question now — the **Test** button in Settings.
The body is an upstream, saved or not: `{"address": "https://9.9.9.9/dns-query", "protocol": "doh"}`
(validated like a saved one; `400` otherwise). The answer:

```json
{"ok": true, "ms": 8.0, "rcode": "NOERROR", "configured": true}
{"ok": false, "ms": 0, "error": "dial tcp 192.0.2.1:853: i/o timeout", "configured": false}
```

`configured` is true when the upstream is one Minos uses; the result then
counts towards its health, so a passing test turns a failing light green at
once. An unsaved upstream is tested on its own and leaves no record.

`last_success`, `last_failure` and `last_error` are omitted until they
happen. Counters are process-lifetime and reset for an address whose
protocol changes, so a corrected entry does not inherit the broken one's
failures. Settings shows green / red / a hollow ring for the three states.

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

### `GET /api/querylog/history?q=&domain=&exclude=&client=&verdict=&qtype=&upstream=&would_block=&list=&dnssec=&since=&before=&limit=`

The persisted log (SQLite), newest first — the full retained history behind
search and the dashboard drill-downs, not just the in-memory ring. `q`
matches a domain or client-IP substring, or a device whose label or
discovered hostname contains it (2+ characters; that device's addresses,
up to 64); `client` is an **exact** address
filter, comma-separated for a device with several IPs (the Devices
drill-down), distinct from the `q` substring; `verdict` is
`blocked`/`allowed`/`all`; `would_block=true` narrows to entries an
audit-mode list flagged; `list` is an **exact** list-name filter matching
either the enforcing attribution (`list`) or the audit one (`audit_list`) —
so it finds what a list blocked, allowed, or would have blocked;
`dnssec` narrows to one validation outcome (the dashboard's counter
drill-down) and rejects anything outside the four with a 400, so a typo
reads as an error rather than as "no matches";
`before` is a unix-millis cursor for "load older"
pagination; `limit` 1–1000. Returns `[]` in ephemeral mode (there the ring
already backs both the log and the dashboard, so the UI filters it directly).

Structured filters, all optional and combined with AND:

| Parameter | Matches |
|---|---|
| `domain` | one **exact** name (case and a trailing dot ignored) — not its subdomains; use `q` for those |
| `exclude` | drops names containing any of these substrings, comma-separated (up to 8) |
| `qtype` | the record type, e.g. `AAAA`, `HTTPS` (case-insensitive) |
| `upstream` | the resolver that answered, exactly as the `upstream` field shows it |
| `since` | unix milliseconds; rows older than this are left out (`before` is the upper bound) |

Every value is validated, so a typo is a 400 rather than an empty result:
`verdict` must be `blocked`, `allowed` or `all`, `qtype` letters and digits,
`domain` a domain name.

### `GET /api/querylog/export?format=csv|json&limit=&<filters>`

Downloads every row matching the same filters as `/history`, newest first,
as one file (`Content-Disposition: attachment`) — up to 100,000 rows, or
`limit`. Read in a single pass, so no rows are lost or repeated the way
page-by-page reading can at a millisecond boundary. CSV (the default) has
the columns `time, client, domain, type, verdict, list, rule, upstream,
duration_ms, would_block_list, would_block_rule, dnssec`, times in UTC;
a cell a spreadsheet would treat as a formula (starting `=`, `+`, `-` or
`@` — blocklist rules such as `@@||example.com^` do) gets a leading `'`.
JSON is an array of entries in the `/history` shape. An export that fails
part-way aborts the connection, so a short file never looks complete.
Ephemeral mode exports nothing (header only, or `[]`).

### `GET /api/querylog/lists?hours=168`

The distinct list names attributed in the window (enforcing and audit),
sorted — the options behind the Query Log's list filter. `hours` 1–2160.
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
"paused_until": "..."}`. Note: pausing does not lift device-level DNS
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
  audit, rules, skipped, state, last_refresh, last_attempt, last_error,
  consecutive_failures, mirrors, served_by, primary_error}`. `served_by` is
  set when the copy in force came from a mirror, with `primary_error`
  saying why the main URL was passed over. `last_refresh` is the last time the publisher was
  reached (a 200 or a 304), so it is the age of the copy in force;
  `last_attempt` is the latest try either way. `state` is the one verdict:

  | state | meaning | still blocking? |
  |---|---|---|
  | `ok` | last check succeeded | yes |
  | `pending` | not downloaded yet | no |
  | `failing` | update failing (network, 5xx, 429, or the server sent a web page); retried on a 1 m → 1 h backoff | yes, last good copy |
  | `stale` | failing for more than two refresh intervals | yes, but the copy is old |
  | `unavailable` | failing and no copy was ever downloaded | **no** |
  | `retired` | 404/410 — the publisher removed it; retried only at the normal interval | yes, last good copy |
  | `unauthorized` | 401/403 — the feed now needs credentials | yes, last good copy |
  | `suspicious` | a download was refused: under half the previous rule count (lists of 100+ rules). The same smaller body three refreshes running is accepted as the list's new size | yes, last good copy |
  | `disabled` | switched off | no |
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
- `mirrors` (on `POST` and `PUT`, up to 5 http(s) URLs) are fallbacks for
  the same list, tried in order only when the main URL is retired (404/410)
  or refuses (401/403), when no copy has ever been downloaded, or when the
  copy in force is stale — never for a passing blip, because a mirror can
  lag its primary. `PUT` with `"mirrors": []` clears them. Catalog entries
  subscribe with their verified mirrors.
- Custom request headers (`headers:` in the config file, e.g. an abuse.ch
  `Auth-Key`) are credentials and are **not** part of the API: they are
  never returned (`has_headers: true` says a list has some), never sent to
  a mirror, and stripped from a redirect to another host. A `PUT` that
  moves such a list's `url` to a different host is refused with 400 — edit
  the config file for that.
- `PUT /api/lists/{name}` — change any of `url`, `format`, `action`,
  `audit`, `enabled`, `mirrors`; changing `action` moves the list between `sources` and
  `allow_sources`
- `DELETE /api/lists/{name}`
- `POST /api/lists/refresh` — refetch everything now (synchronous)

## Services

- `GET /api/services` — `{"catalog": [{name, label, domains}],
  "blocked": [...], "allowed": [...]}`. `blocked` services are denied for
  everyone; `allowed` services are allowed for everyone — every domain the
  service needs, including playback/sign-in hosts the deny bundle doesn't
  carry. Allow beats deny, so a service that is both ends up allowed.
- `PUT /api/services` — partial update; each present field replaces its
  set, omitted fields stay untouched: `{"blocked": ["tiktok"]}`,
  `{"allowed": ["netflix"]}`, or both.

### Custom services

User-defined bundles that behave like catalog services everywhere —
`GET /api/services` returns them as `"custom": [{name, label, domains,
allow_extra, blocked, allowed}]`. Unlike catalog services, a custom's
**global** block/allow toggles live on the definition itself (`blocked`/
`allowed`), never inside the `blocked`/`allowed` arrays above — those are
validated against the compiled-in catalog, and keeping custom names out of
them is what lets an older binary still boot this config (it drops the
unknown `custom_services` keys whole).

- `POST /api/services/custom` — `{"label", "domains": [...], "name"?,
  "allow_extra"?, "blocked"?, "allowed"?}`. `name` (a lowercase slug, max
  40 chars) is derived from the label when omitted; a clash with a
  catalog service name is rejected. `allow_extra` lists hosts allowed
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
  (most recently active) address and `ips` lists them all (used by the Query Log
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
  `services` are blocked and `allowed_services` allowed for members only;
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

### Home Assistant (MQTT)

`GET /api/config` reports `home_assistant` (`enabled`, `broker`,
`username`, `discovery_prefix`, `topic_prefix`, `device_switches`, and
`password_set` in place of the password); `PUT /api/config` takes the same
fields plus `password` (omit it to keep the stored one). The connection
follows the settings live.

`GET /api/home-assistant/status` →
`{"enabled": true, "connected": false, "error": "the broker refused the
connection: not authorized (check the username and password)", "since":
"…"}` — `error` is the reason the last connection ended or failed; Minos
keeps retrying (up to a minute apart). The topics and entities are listed
in [home-assistant.md](home-assistant.md).

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

## Application log

Minos's own log — the lines that also go to the journal — held in memory
(the newest 2,000, from this run) with secrets redacted: attributes named
like credentials (`token`, `password`, `key`, `auth`, `secret`, …) become
`[redacted]`, and URL passwords and `token=`/`key=` query values become
`xxxxx`. The journal output is redacted the same way.

### `GET /api/logs?level=&q=&after=`

```json
{"level": "info",
 "records": [{"seq": 41, "time": "2026-10-23T18:15:28.041+01:00", "level": "warn",
              "message": "list fetch failed",
              "attrs": [{"key": "list", "value": "URLhaus"}, {"key": "err", "value": "…"}]}],
 "alert_seq": 41}
```

Records oldest first. `level` narrows to that level and above; `q` matches
the message or any `key=value`; `after` returns only records with a larger
`seq` (for polling — `minos logs -f` uses it). The response's `level` is
what is being recorded now; `alert_seq` is the newest warning/error (also
`log_alert_seq` on `/api/status`, which the UI's menu dot compares with the
last one seen). Sequence numbers restart at 1 when Minos restarts.

### `GET /api/logs/stream` (WebSocket)

Each new record as a JSON frame, same shape.

### `GET /api/logs/download?level=&q=`

The held records as a text file (`Content-Disposition: attachment`), one
journal-style line each.

The level to record is `log_level` in `PUT /api/config` (`debug`, `info`,
`warn`, `error`), applied live. It is saved in the config, and wins over
the `-log-level` flag's default but not over a flag passed explicitly.

## Notifications (outbound)

Configured via `notifications` in the config or Settings; each event is
POSTed to your webhook as:

```json
{"type": "device_new | upstream_sick | upstream_recovered | dns_down | list_failing | list_recovered | update_available | acme_renewal_failed | digest | test",
 "title": "New device: Kitchen speaker",
 "message": "Kitchen speaker made its first DNS query through Minos (made by Sonos, address 192.168.1.77, MAC aa:bb:cc:dd:ee:ff). …",
 "time": "2026-07-05T10:12:03Z",
 "url": "http://192.168.1.2:8080/#/device/aa:bb:cc:dd:ee:ff",
 "priority": 3,
 "actions": [{"label": "Open device", "url": "http://192.168.1.2:8080/#/device/aa:bb:cc:dd:ee:ff"}]}
```

`url` is where to act on the event in Minos (absent when Minos cannot work
out its own address and `notifications.ui_url` is unset); `priority` is
ntfy's 1–5 scale — `dns_down` is 5 and ignores quiet hours, which lower
everything else to 2. ntfy topics receive the same event through ntfy's
JSON publishing (title, message, tags, priority, `click`, view actions).

### `POST /api/notifications/test`

Sends a test event to every configured destination now (bypassing the
queue and `mute`) and answers `{"webhook": "ok", "ntfy": "rejected: 403
Forbidden"}` — one entry per destination. 400 when none is configured.

`GET /api/config` reports `notifications.ui_url`, `ui_url_default` (the
derived address links use when `ui_url` is blank), `quiet_hours`, `mute`,
and `types` (every value `mute` accepts); `PUT /api/config` takes `ui_url`,
`quiet_hours` (`"HH:MM-HH:MM"`, may wrap midnight, `""` to clear) and
`mute`.

`list_failing` fires once when a blocklist becomes retired (404/410) or
unauthorized (401/403), or — after it has failed three attempts running, so
a slow boot never pages anyone — unavailable or stale; `list_recovered`
fires when a list that was reported comes back. Minos keeps blocking with
the last good copy throughout.

`digest` events are the opt-in traffic summary (`notifications.digest:
daily|weekly`): totals, block rate, top blocked domains, busiest client,
and new-device count for the period, as a plain-text `message`.

## Errors

Non-2xx responses carry `{"error": "plain description"}` — 400 for invalid
input (validation failures apply nothing), 401 for a missing/wrong token,
404 for unknown names/IPs.
