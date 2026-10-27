// Command dnsload measures a running Minos's DNS throughput, for comparing
// releases on the same machine. It brings its own world so nothing outside
// is involved: a stand-in upstream resolver that answers instantly, and a
// blocklist served over HTTP.
//
//	dnsload -serve -upstream 127.0.0.1:25300 -lists 127.0.0.1:25380
//	# point Minos at upstream 127.0.0.1:25300 (udp) and list http://127.0.0.1:25380/hosts.txt
//	dnsload -target 127.0.0.1:25353 -workers 32 -duration 30s            # as fast as it answers
//	dnsload -target 127.0.0.1:25353 -workers 32 -duration 30s -rate 3000 # a fixed load
//
// The stress mix is 40% blocked names from the list, 40% a small set of
// allowed names (cache hits after the first) and 20% names never seen
// before (forwarded upstream) — hard on the query log, whose cost grows
// with distinct names. The home mix (-mix home) is what a household sends:
// a few thousand names, Zipf-distributed, 15% blocked, 2% new. It prints queries per second, latency
// percentiles, and — given -pid — the CPU time Minos spent per 1,000
// lookups, which is the figure least disturbed by whatever else the
// machine is doing.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// listSize is how many names the served list holds (-size); the load
// draws its blocked names from them.
var listSize = 200_000

func blockedName(i int) string { return fmt.Sprintf("ad%d.tracker%d.test", i, i%997) }

func main() {
	serve := flag.Bool("serve", false, "run the stand-in upstream and list server")
	upstream := flag.String("upstream", "127.0.0.1:25300", "stand-in upstream address (-serve)")
	lists := flag.String("lists", "127.0.0.1:25380", "list server address (-serve)")
	flag.IntVar(&listSize, "size", listSize, "names in the served list (-serve), and the range blocked names are drawn from")
	target := flag.String("target", "127.0.0.1:25353", "Minos DNS address to load")
	workers := flag.Int("workers", 32, "concurrent clients")
	duration := flag.Duration("duration", 30*time.Second, "how long to load")
	pid := flag.Int("pid", 0, "Minos process ID, for CPU time per lookup (Linux)")
	rate := flag.Int("rate", 0, "total lookups per second to send (0: as fast as answers come)")
	mix := flag.String("mix", "stress", "stress (most names new or rarely repeated) or home (a few thousand names, popular ones most of the time)")
	flag.Parse()
	if *serve {
		runServers(*upstream, *lists)
		return
	}
	runLoad(*target, *workers, *duration, *pid, *rate, *mix)
}

func runServers(upstream, lists string) {
	h := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) == 1 && r.Question[0].Qtype == dns.TypeA {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
				A:   net.IPv4(192, 0, 2, 1),
			})
		}
		_ = w.WriteMsg(m)
	})
	for _, n := range []string{"udp", "tcp"} {
		srv := &dns.Server{Addr: upstream, Net: n, Handler: h}
		go func() { log.Fatal(srv.ListenAndServe()) }()
	}
	var body strings.Builder
	body.WriteString("# dnsload test list\n")
	for i := range listSize {
		fmt.Fprintf(&body, "0.0.0.0 %s\n", blockedName(i))
	}
	list := body.String()
	http.HandleFunc("/hosts.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(list))
	})
	log.Printf("upstream on %s, list on http://%s/hosts.txt (%d names)", upstream, lists, listSize)
	log.Fatal(http.ListenAndServe(lists, nil))
}

// cpuSeconds reads a process's user+system CPU time from /proc.
func cpuSeconds(pid int) float64 {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	f := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+2:]))
	utime, _ := strconv.ParseFloat(f[11], 64)
	stime, _ := strconv.ParseFloat(f[12], 64)
	return (utime + stime) / 100 // USER_HZ
}

func runLoad(target string, workers int, duration time.Duration, pid, rate int, mix string) {
	var unique atomic.Uint64
	var ok, failed atomic.Int64
	var mu sync.Mutex
	var lat []time.Duration
	deadline := time.Now().Add(duration)
	cpu0 := cpuSeconds(pid)
	start := time.Now()
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(w), 7))
			// Home traffic: Zipf-distributed over a few thousand names, as a
			// household's lookups are.
			allowedZ := rand.NewZipf(r, 1.1, 1, 2999)
			blockedZ := rand.NewZipf(r, 1.1, 1, 299)
			conn, err := dns.Dial("udp", target)
			if err != nil {
				log.Fatal(err)
			}
			defer func() { _ = conn.Close() }() // The latest connection
			var mine []time.Duration
			// With a rate, each worker paces its share (open loop: a slow
			// answer delays only that worker's next send).
			var every time.Duration
			if rate > 0 {
				every = time.Duration(float64(time.Second) * float64(workers) / float64(rate))
			}
			next := time.Now()
			for time.Now().Before(deadline) {
				if every > 0 {
					if wait := time.Until(next); wait > 0 {
						time.Sleep(wait)
					}
					next = next.Add(every)
				}
				var name string
				p := r.IntN(100)
				if mix == "home" {
					switch {
					case p < 15:
						name = blockedName(int(blockedZ.Uint64()))
					case p < 98:
						name = fmt.Sprintf("site%d.example.org", allowedZ.Uint64())
					default:
						name = fmt.Sprintf("u%d.example.net", unique.Add(1))
					}
				}
				switch {
				case name != "":
				case p < 40:
					name = blockedName(r.IntN(listSize))
				case p < 80:
					name = fmt.Sprintf("site%d.example.org", r.IntN(500))
				default:
					name = fmt.Sprintf("u%d.example.net", unique.Add(1))
				}
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn(name), dns.TypeA)
				t := time.Now()
				_ = conn.SetDeadline(t.Add(2 * time.Second))
				if err := conn.WriteMsg(m); err != nil {
					failed.Add(1)
					continue
				}
				resp, err := conn.ReadMsg()
				if err != nil || resp.Id != m.Id {
					failed.Add(1)
					// A timed-out socket may deliver the late answer next: start afresh.
					_ = conn.Close()
					if conn, err = dns.Dial("udp", target); err != nil {
						log.Fatal(err)
					}
					continue
				}
				mine = append(mine, time.Since(t))
				ok.Add(1)
			}
			mu.Lock()
			lat = append(lat, mine...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	cpu1 := cpuSeconds(pid)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[min(len(lat)-1, int(p*float64(len(lat))))]
	}
	fmt.Printf("lookups %d in %s: %.0f per second, %d failed\n", ok.Load(), elapsed.Round(time.Millisecond),
		float64(ok.Load())/elapsed.Seconds(), failed.Load())
	fmt.Printf("latency p50 %s  p95 %s  p99 %s  max %s\n", pct(0.50), pct(0.95), pct(0.99), pct(1))
	if pid > 0 && cpu0 >= 0 && cpu1 >= 0 && ok.Load() > 0 {
		fmt.Printf("minos CPU %.2f s: %.1f ms per 1,000 lookups\n", cpu1-cpu0, 1000*(cpu1-cpu0)/float64(ok.Load())*1000)
	}
}
