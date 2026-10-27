#!/bin/sh
# Benchmarks a Minos binary on this machine — the method behind
# docs/benchmarks.md. Needs a dnsload binary (go build ./scripts/dnsload)
# beside it. Everything runs from a scratch directory (default /dev/shm, so
# an SD card is not written), on loopback ports 25300–25380 and 28080, with
# a stand-in upstream that answers instantly: the figures are Minos's own
# cost, not the network's. Nothing installed is touched.
#
#   sh scripts/bench.sh ./minos ./dnsload
set -u
MINOS=$(readlink -f "$1")
LOAD=$(readlink -f "$2")
W=${BENCH_DIR:-/dev/shm/minos-bench}
rm -rf "$W"; mkdir -p "$W"; cd "$W" || exit 1

config() { # $1: directory
	cat > "$1/minos.yaml" <<YAML
dns:
  listen: 127.0.0.1:25353
  upstreams:
    - address: 127.0.0.1:25300
      protocol: udp
  rate_limit: 0
lists:
  sources:
    - name: bench
      url: http://127.0.0.1:25380/hosts.txt
      format: hosts
      enabled: true
querylog:
  db_path: $1/minos.db
api:
  listen: 127.0.0.1:28080
discovery:
  ssdp: false
  dhcp_listen: false
YAML
}

start() { # $1: directory, $2: rules expected; sets PID
	"$MINOS" serve -config "$1/minos.yaml" >"$1/run.log" 2>&1 &
	PID=$!
	for _ in $(seq 1 180); do grep -q "matcher rebuilt.*rules=$2 " "$1/run.log" && return; sleep 1; done
	echo "minos never loaded $2 rules"; exit 1
}

mem() { awk '/VmRSS/ {r=$2} /VmHWM/ {h=$2} END {printf "%.0f MB (peak %.0f MB)", r/1024, h/1024}' "/proc/$1/status"; }

stop() { kill "$PID"; wait "$PID" 2>/dev/null; }

echo "## $("$MINOS" version) on $(tr -d '\0' </proc/device-tree/model 2>/dev/null || uname -m)"

echo "### Memory and rebuild time by list size"
for size in 200000 1000000 2000000; do
	"$LOAD" -serve -size "$size" >serve.log 2>&1 &
	SERVE=$!
	sleep 3
	D=$W/mem-$size; mkdir -p "$D"; config "$D"; start "$D" "$size"
	took=$(grep -o "matcher rebuilt.*" "$D/run.log" | grep -o "took=[^ ]*" | tail -1)
	sleep 180 # Let the runtime return freed memory
	echo "$size rules: load $took, memory $(mem "$PID") after 3 minutes"
	stop; kill "$SERVE"; wait "$SERVE" 2>/dev/null
done

echo "### Throughput and latency (200k-rule list, stand-in upstream)"
"$LOAD" -serve >serve.log 2>&1 &
SERVE=$!
sleep 3
for run in "home 100" "home 3000" "stress 3000" "home 0" "stress 0"; do
	set -- $run
	D=$W/run-$1-$2; mkdir -p "$D"; config "$D"; start "$D" 200000
	"$LOAD" -workers 16 -duration 10s -mix "$1" -rate "$2" >/dev/null # Warm-up
	label="$1 mix, $2 lookups/s"; [ "$2" = 0 ] && label="$1 mix, flat out"
	echo "#### $label"
	"$LOAD" -workers 16 -duration 30s -mix "$1" -rate "$2" -pid "$PID"
	echo "memory $(mem "$PID"), query-log writer behind: $(grep -c 'writer is behind' "$D/run.log") times"
	stop
done
kill "$SERVE"; wait "$SERVE" 2>/dev/null
cd /; rm -rf "$W"
