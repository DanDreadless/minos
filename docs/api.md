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
  "version": "0.3.0",
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

## Blocklists

- `GET /api/lists` — per-list status: `{name, url, format, enabled, rules,
  skipped, last_refresh, last_error}`
- `POST /api/lists` — add: `{"name", "url", "format": "hosts|plain|adblock",
  "enabled": true}` (fetches immediately)
- `PUT /api/lists/{name}` — change any of `url`, `format`, `enabled`
- `DELETE /api/lists/{name}`
- `POST /api/lists/refresh` — refetch everything now (synchronous)

## Blocked services

- `GET /api/services` — `{"catalog": [{name, label, domains}], "blocked": [...]}`
- `PUT /api/services` — replace the global set: `{"blocked": ["tiktok", "youtube"]}`

## Devices & groups

- `GET /api/clients` — every device that has queried plus every configured
  one: `{ip, mac, hostname, name, group, blocked, seen, queries,
  queries_blocked, first_seen, last_seen}`
- `PUT /api/clients/{ip}` — upsert any of `{"name", "mac", "group",
  "blocked"}` (`"group": "default"` unassigns)
- `DELETE /api/clients/{ip}` — forget the saved assignment
- `GET /api/groups` / `POST /api/groups` — groups are `{name, mode:
  "filter|bypass|block", allowlist, denylist, services, safe_search,
  schedule}`; `schedule` is `{days: ["mon", ...], start: "21:00",
  end: "07:00"}` or `null` to clear
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
