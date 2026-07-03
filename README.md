# Minos

> Every query gets judged.

Minos is a modern, user-friendly DNS sinkhole (a Pi-hole alternative) written
in Go — a single static binary with an embedded web UI, light enough for a
Raspberry Pi. Named for the judge of the underworld: every DNS query that
arrives is judged against your blocklists and sentenced — no exceptions,
no appeals (well, except pardons).

## What it does

- Listens for DNS queries on `:53` (UDP + TCP)
- Judges each query against compiled blocklists (hosts, plain, and AdBlock formats)
- Blocked queries get `0.0.0.0`/`::` or `NXDOMAIN` (your choice)
- Allowed queries are forwarded upstream over DoH or DoT (plaintext optional)
- Live query log streamed to the web UI, with one-click pardons
- Batched SQLite persistence that respects SD cards
- No telemetry. Ever.

## Quick start

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
```

For local development without root, set `dns.listen: ":5353"` in the config
and test with `dig @127.0.0.1 -p 5353 doubleclick.net`.

## Deploying

See `deploy/` for a multi-arch Dockerfile, a compose example, and a hardened
systemd unit. Docs live in `docs/`.

## Developing

Go 1.22+, Node 20+. `make test` runs the Go suite with the race detector;
`make lint` runs golangci-lint and the frontend type check; `make bench`
runs the filter engine benchmarks. See `CLAUDE.md` for architecture,
conventions, and performance budgets.

## License

GPLv3.
