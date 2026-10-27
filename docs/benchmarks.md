# Benchmarks

What Minos costs on the hardware it is built for: a **Raspberry Pi 4 Model B
(8 GB)** running Debian 13, Minos 0.37.3 (dashboard times: 0.37.4). Measured on 27 October 2026.

## Method

Everything is reproducible with two files in this repository:

- [`scripts/dnsload`](../scripts/dnsload/main.go) — a load generator that
  brings its own world: a stand-in upstream resolver that answers
  instantly, and a blocklist of any size served over HTTP. So the figures
  are Minos's own cost, not the network's or an upstream's.
- [`scripts/bench.sh`](../scripts/bench.sh) — runs a throwaway Minos from
  `/dev/shm` (nothing installed is touched, the SD card is not written)
  against it, with the query log on, and reports the numbers below.

```sh
go build -o dnsload ./scripts/dnsload
sh scripts/bench.sh /usr/local/bin/minos ./dnsload
```

Two lookup mixes:

- **Household** — what a home sends: a few thousand names, a small popular
  set most of the time (Zipf), 15% blocked, 2% never seen before.
- **Stress** — 40% blocked names drawn from the whole list, 40% repeats,
  20% never seen before. Hard on the query log, whose cost grows with
  distinct names.

"CPU per 1,000 lookups" is Minos's whole process — answering, logging,
statistics, threat detection — and is the steadiest figure on a shared
machine. Latency is measured at the client, over loopback.

## Answering lookups

With a 200,000-rule blocklist:

| Load | Lookups/s | Median | p95 | p99 | CPU per 1,000 lookups |
|---|---|---|---|---|---|
| Household, 100/s | 100 | 0.88 ms | 2.5 ms | 3.3 ms | — ¹ |
| Household, 3,000/s | 3,000 | 0.53 ms | 1.2 ms | 1.9 ms | 259 ms |
| Stress, 3,000/s | 3,000 | 0.81 ms | 2.1 ms | 4.1 ms | 381 ms |
| Household, flat out | **16,700** | 0.74 ms | 2.4 ms | 5.0 ms | 142 ms |
| Stress, flat out | **9,900** | 1.27 ms | 4.2 ms | 6.9 ms | 247 ms |

¹ At 100 lookups a second the per-lookup CPU figure is dominated by
Minos's fixed background work and means little; for scale, a household
sends a few lookups a second on average.

Flat out, the query log's writer cannot keep every entry and drops the
oldest batches rather than slow lookups down (it says so in the
application log); at 3,000 a second and below, it keeps every one.

## Memory

| Blocklist rules | Load / rebuild | Memory, settled | Memory, peak while rebuilding |
|---|---|---|---|
| 200,000 | 1.0 s | 26 MB | 52 MB |
| 1,000,000 | 6.0 s | 53 MB | 224 MB |
| 2,000,000 | 13.3 s | 88 MB | 456 MB |

A rebuild happens when lists refresh (daily by default) and on every
change to the rules — allowing a domain, say. Peak memory is the new rule
set being built beside the one in use; it is returned straight away.
Memory with the query log and live traffic: 57 MB at 3,000 lookups a
second (household mix).

## Query log, search and statistics

On the Pi's real query log: 90 days of a household's lookups, 3.1 GB on
the SD card. Each figure is the median of three requests.

| Request | Time |
|---|---|
| Query Log, newest 100 | 12 ms |
| Search for a common name (apple.com) | 103 ms |
| Search for a name never looked up | 47 ms |
| Search for blocked lookups of "netflix" | 60 ms |
| Dashboard, 24 hours | 98 ms |
| Dashboard, 7 days | 159 ms |
| Dashboard, 30 days | 179 ms |
| Dashboard, 90 days | 325 ms |
| Privacy report, whole network, 7 days | 304 ms |
| Blocklist value report, 30 days | 196 ms |

## Against other DNS filters

Not measured yet. The same `bench.sh` method — identical blocklist,
identical stand-in upstream, the same machine — applies to Pi-hole and
AdGuard Home; running them needs them installed on the test Pi, which
this round did not do. Until then, nothing here compares Minos with them.

## History

- **v0.37.2** fixed a regression in v0.32–v0.37: the "something may be
  broken" detector scanned its whole table for every new blocked name
  once the table filled, which cost a third of peak throughput. Peak
  rose from 6,700 to 9,600 lookups a second (stress mix).
- **v0.37.3** stopped keeping downloaded list text in memory beside the
  compiled rules: with 2 million rules, settled memory fell from 242 MB
  to 88 MB.
- **v0.37.4** added daily statistics tables: the 90-day dashboard went from
  1.8 s to 0.33 s and the 30-day from 0.65 s to 0.18 s on the same log.
