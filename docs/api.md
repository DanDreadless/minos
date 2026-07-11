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
  "update_available": false
}
```

`paused_until` (RFC 3339) appears during a timed pause; `latest_version`
appears once the opt-in update check has run.

### `GET /api/update`

The running and latest versions plus an actionable upgrade command for how
*this* instance was installed (detected at runtime):

```json
{"current": "0.7.0", "latest": "0.8.0", "available": true,
 "install_method": "binary",
 "command": "curl -fsSL https://raw.githubusercontent.com/DanDreadless/minos/main/deploy/install.sh | sudo sh && sudo systemctl restart minos",
 "notes_url": "https://github.com/DanDreadless/minos/releases/tag/v0.8.0"}
```

`install_method` is `binary` (quick-install), `docker`, or `source`.
Display-only — Minos never runs the command itself.

### `GET /api/stats?hours=24`

Dashboard aggregates for a 1–168 hour window: a `timeline` of
`{time, total, blocked}` buckets (10-minute buckets up to 24 h, hourly
beyond), `top_blocked` as `{qname, count}`, and `top_clients` as
`{client, total, blocked}`. Entries not yet flushed to disk (up to 30 s)
are not included.

### `GET /api/check?domain=ads.example.com`

Judge a name without querying it:

```json
{"domain": "ads.example.com", "verdict": "blocked", "list": "StevenBlack", "rule": "ads.example.com"}
```

`verdict` is `blocked` or `allowed`; `list`/`rule` say which rule decided
(empty when no rule matched). The check ignores an active pause on purpose.

## Query log

### `GET /api/querylog?limit=100`

Newest first, `limit` 1–10000, from the in-memory ring:

```json
[{"time": "2026-07-05T10:12:03Z", "client": "192.168.1.50",
  "qname": "doubleclick.net", "qtype": "A", "verdict": "blocked",
  "list": "StevenBlack", "rule": "doubleclick.net", "duration_ms": 0.21}]
```

Allowed entries carry `upstream` instead of `list`/`rule` — the resolver
that answered, or `cache`, `stale`, `local`, or `safesearch`.

### `GET /api/querylog/history?q=&client=&verdict=&before=&limit=`

The persisted log (SQLite), newest first — the full retained history behind
search and the dashboard drill-downs, not just the in-memory ring. `q`
matches a client IP or domain substring; `client` is an **exact** address
filter, comma-separated for a device with several IPs (the Devices
drill-down), distinct from the `q` substring; `verdict` is
`blocked`/`allowed`/`all`; `before` is a unix-millis cursor for "load older"
pagination; `limit` 1–1000. Returns `[]` in ephemeral mode (there the ring
already backs both the log and the dashboard, so the UI filters it directly).

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
  rules, skipped, last_refresh, last_error}`
- `POST /api/lists` — add: `{"name", "url", "format": "hosts|plain|adblock",
  "action": "block|allow", "enabled": true}` (fetches immediately). `action`
  defaults to `block`; `allow` makes the source a subscribed allowlist —
  every entry is always allowed, beating any blocklist, and a passing
  verdict names it in the query log. In an `allow` list, block-shaped
  AdBlock rules count as allows too (membership decides meaning, matching
  AdGuard whitelist filters and Pi-hole v6 antigravity lists). In the
  config file, allowlists live under `lists.allow_sources` (block lists
  under `lists.sources`); names are unique across both.
- `PUT /api/lists/{name}` — change any of `url`, `format`, `action`,
  `enabled`; changing `action` moves the list between `sources` and
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

## Devices & groups

- `GET /api/clients` — every device that has queried plus every configured
  one, **one row per physical device**: `{ip, ips, mac, vendor, hostname,
  name, group, blocked, seen, queries, queries_blocked, first_seen,
  last_seen}`. A device is identified by its MAC when known, so all the IPs
  it has held across DHCP leases fold into one entry — `ip` is the primary
  (most recently active) address and `ips` lists them all (used by the Docket
  drill-down); counts are summed across them. `vendor` is derived from the MAC
  (OUI); `hostname` comes from reverse DNS via the gateway, falling back to
  NetBIOS then mDNS `.local` — all best-effort.
- `PUT /api/clients/{key}` — upsert any of `{"name", "mac", "group",
  "blocked"}` (`"group": "default"` unassigns). `{key}` is the device's **MAC**
  when it has one (so the assignment follows it across DHCP leases) or its
  **IP** otherwise; a MAC key resolves the device's current IP automatically,
  or accepts an `"ip"` field as a last-known-address hint when it's offline.
- `DELETE /api/clients/{key}` — forget the saved assignment (`{key}` is the
  MAC or IP, as above)
- `GET /api/groups` / `POST /api/groups` — groups are `{name, mode:
  "filter|bypass|block", allowlist, denylist, services, allowed_services,
  safe_search, schedule}`; `services` are blocked and `allowed_services`
  pardoned for members only; `schedule` is `{days: ["mon", ...], start:
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
{"type": "device_new | upstream_sick | upstream_recovered | update_available",
 "title": "New device on your network",
 "message": "192.168.1.77 (phone.lan) [aa:bb:cc:dd:ee:ff] made its first DNS query through Minos.",
 "time": "2026-07-05T10:12:03Z"}
```

## Errors

Non-2xx responses carry `{"error": "plain description"}` — 400 for invalid
input (validation failures apply nothing), 401 for a missing/wrong token,
404 for unknown names/IPs.
