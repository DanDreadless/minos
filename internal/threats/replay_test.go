//go:build replay

package threats

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"minos/internal/querylog"
)

// TestReplay feeds a real query log through the detectors to measure false
// positives before shipping a threshold change. Read-only; prints counts and
// flagged registrable domains only — never device addresses.
//
//	MINOS_DB=/var/lib/minos/querylog.db MINOS_HOURS=72 go test -tags replay -run TestReplay ./internal/threats
func top(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	out := []string{}
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, fmt.Sprintf("%s x%d", all[i].k, all[i].v))
	}
	return out
}

func TestReplay(t *testing.T) {
	path := os.Getenv("MINOS_DB")
	if path == "" {
		t.Skip("MINOS_DB not set")
	}
	hours, _ := strconv.Atoi(os.Getenv("MINOS_HOURS"))
	if hours <= 0 {
		hours = 72
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	since := time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
	rows, err := db.Query(`SELECT ts, client, qname, qtype, verdict, list FROM querylog WHERE ts >= ? ORDER BY ts`, since)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	clients := map[string]int{}
	var raised []Finding
	d := New(Options{
		ThreatList: func(l string) bool { return ThreatFeed(l, "") },
		OnNew:      func(f Finding) { raised = append(raised, f) },
	})
	// Shapes of tunnel-like names: label lengths and query types only.
	shapes := map[string]map[string]int{}
	qtypes := map[string]map[string]int{}
	n := 0
	for rows.Next() {
		var ts int64
		var e querylog.Entry
		if err := rows.Scan(&ts, &e.Client, &e.QName, &e.QType, &e.Verdict, &e.List); err != nil {
			t.Fatal(err)
		}
		e.Time = time.UnixMilli(ts)
		d.Observe(e)
		if reg, sub, ok := split(e.QName); ok && tunnelLike(sub) {
			if shapes[reg] == nil {
				shapes[reg], qtypes[reg] = map[string]int{}, map[string]int{}
			}
			ls := []string{}
			for _, l := range strings.Split(sub, ".") {
				ls = append(ls, strconv.Itoa(len(l)))
			}
			shapes[reg][strings.Join(ls, ".")]++
			qtypes[reg][e.QType]++
		}
		n++
		if n%200000 == 0 {
			d.sweep(e.Time) // Keep memory bounded as the live loop would
		}
	}
	fmt.Printf("replayed %d lookups over %d h\n", n, hours)
	time.Sleep(100 * time.Millisecond)
	// Every alert raised over the period (findings expire, so count these).
	perKind := map[Kind]int{}
	devices := map[Kind]map[string]bool{}
	weeks := map[Kind]map[int]int{}
	for _, f := range raised {
		perKind[f.Kind]++
		if devices[f.Kind] == nil {
			devices[f.Kind], weeks[f.Kind] = map[string]bool{}, map[int]int{}
		}
		devices[f.Kind][f.Client] = true
		weeks[f.Kind][int(time.Since(f.First).Hours()/168)]++
	}
	for _, k := range []Kind{KindDGA, KindTunnel, KindKnown} {
		fmt.Printf("raised %s: %d alerts, %d devices, by week ago %v\n", k, perKind[k], len(devices[k]), weeks[k])
	}
	seen := map[string]bool{}
	for _, f := range raised {
		if f.Kind == KindTunnel && !seen[f.Domain] {
			seen[f.Domain] = true
			fmt.Printf("tunnel shape %s: label lengths %v qtypes %v\n", f.Domain, top(shapes[f.Domain], 6), qtypes[f.Domain])
		}
	}
	byKind := map[Kind][]Finding{}
	for _, f := range d.findings { // Every finding raised, including expired ones swept? (live ones only)
		byKind[f.Kind] = append(byKind[f.Kind], *f)
	}
	for _, k := range []Kind{KindDGA, KindTunnel, KindKnown} {
		fs := byKind[k]
		sort.Slice(fs, func(i, j int) bool { return fs[i].Count > fs[j].Count })
		fmt.Printf("%s: %d findings\n", k, len(fs))
		for _, f := range fs {
			if _, ok := clients[f.Client]; !ok {
				clients[f.Client] = len(clients) + 1
			}
			doms := []string{}
			for _, s := range f.Samples {
				if reg, _, ok := split(s); ok {
					doms = append(doms, reg)
				}
			}
			fmt.Printf("  device#%d count=%d domain=%s list=%s samples=%v\n", clients[f.Client], f.Count, f.Domain, f.List, doms)
		}
	}
}
