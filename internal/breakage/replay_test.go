//go:build replay

package breakage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/publicsuffix"
	_ "modernc.org/sqlite"

	"minos/internal/querylog"
)

// TestReplay feeds a real query log through the detector twice — with the
// old rule (a burst after a quiet day) and the current one (a burst of a
// name that used to work) — to see what each would have announced. Read-
// only; prints counts, lists and registrable domains only, never devices.
//
//	MINOS_DB=/var/lib/minos/querylog.db MINOS_HOURS=720 go test -tags replay -run TestReplay ./internal/breakage
func TestReplay(t *testing.T) {
	path := os.Getenv("MINOS_DB")
	if path == "" {
		t.Skip("MINOS_DB not set")
	}
	hours, _ := strconv.Atoi(os.Getenv("MINOS_HOURS"))
	if hours <= 0 {
		hours = 720
	}
	// The detector logs each suggestion with the device's address; keep
	// those out of the replay's output.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	blockedBefore := func(ctx context.Context, client, domain string, start time.Time) int {
		var n int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM querylog WHERE qname = ? AND verdict = 'blocked'
			AND ts >= ? AND ts < ? AND client = ? LIMIT ?)`, domain, start.Add(-24*time.Hour).UnixMilli(),
			start.UnixMilli(), client, ChronicLimit+1).Scan(&n)
		return n
	}
	worked := func(ctx context.Context, client, domain string, start time.Time) (int, time.Time) {
		var n int
		var last sql.NullInt64
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(ts) FROM (SELECT ts FROM querylog WHERE qname = ? AND verdict = 'allowed'
			AND ts >= ? AND ts < ? AND client = ? AND (list = '' OR list IS NULL) LIMIT ?)`, domain,
			start.Add(-WorkedWindow).UnixMilli(), start.UnixMilli(), client, WorkedCap).Scan(&n, &last)
		return n, time.UnixMilli(last.Int64)
	}

	type result struct {
		mu  sync.Mutex
		got []Suggestion
	}
	var oldRule, newRule result
	old := New(func(ctx context.Context, client, domain string, start time.Time) (Evidence, error) {
		return Evidence{Worked: MinWorked, Blocked: blockedBefore(ctx, client, domain, start)}, nil
	})
	old.OnNew(func(s Suggestion) { oldRule.mu.Lock(); oldRule.got = append(oldRule.got, s); oldRule.mu.Unlock() })
	cur := New(func(ctx context.Context, client, domain string, start time.Time) (Evidence, error) {
		ev := Evidence{Blocked: blockedBefore(ctx, client, domain, start)}
		ev.Worked, ev.WorkedLast = worked(ctx, client, domain, start)
		return ev, nil
	})
	cur.OnNew(func(s Suggestion) { newRule.mu.Lock(); newRule.got = append(newRule.got, s); newRule.mu.Unlock() })

	since := time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
	rows, err := db.Query(`SELECT ts, client, qname, verdict, list FROM querylog WHERE ts >= ? ORDER BY ts`, since)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	var lastSweep time.Time
	settle := func(d *Detector) {
		for {
			d.mu.Lock()
			busy := len(d.pending) > 0
			d.mu.Unlock()
			if !busy {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	for rows.Next() {
		var ts int64
		var e querylog.Entry
		if err := rows.Scan(&ts, &e.Client, &e.QName, &e.Verdict, &e.List); err != nil {
			t.Fatal(err)
		}
		e.Time = time.UnixMilli(ts)
		old.observe(context.Background(), e)
		cur.observe(context.Background(), e)
		if e.Time.Sub(lastSweep) > time.Minute {
			settle(old)
			settle(cur)
			old.sweep(e.Time)
			cur.sweep(e.Time)
			lastSweep = e.Time
		}
		n++
	}
	settle(old)
	settle(cur)

	report := func(name string, r *result) {
		byDomain := map[string]int{}
		for _, s := range r.got {
			reg, err := publicsuffix.EffectiveTLDPlusOne(s.Domain)
			if err != nil {
				reg = s.Domain
			}
			byDomain[reg+" ("+s.List+")"]++
		}
		keys := make([]string, 0, len(byDomain))
		for k := range byDomain {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return byDomain[keys[i]] > byDomain[keys[j]] })
		fmt.Printf("%s: %d alerts\n", name, len(r.got))
		for i, k := range keys {
			if i == 25 {
				fmt.Printf("  … %d more\n", len(keys)-25)
				break
			}
			fmt.Printf("  %3d  %s\n", byDomain[k], k)
		}
	}
	fmt.Printf("replayed %d lookups over %d hours\n", n, hours)
	report("old rule (burst after a quiet day)", &oldRule)
	report("new rule (burst of a name that used to work)", &newRule)
	for _, s := range newRule.got {
		reg, _ := publicsuffix.EffectiveTLDPlusOne(s.Domain)
		fmt.Printf("  new: %s (%s) worked %d times, last %s before the burst\n", reg, s.List, s.Worked,
			s.Since.Sub(s.WorkedLast).Round(time.Minute))
	}
}
