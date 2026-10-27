//go:build replay

package trackers

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestReplay classifies a real query log's last week by company, to check
// the tracking/other split before shipping a data update. Read-only; prints
// company totals and names only, never devices.
//
//	MINOS_DB=/var/lib/minos/minos.db go test -tags replay -run TestReplay ./internal/trackers
func TestReplay(t *testing.T) {
	path := os.Getenv("MINOS_DB")
	if path == "" {
		t.Skip("MINOS_DB not set")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Now().AddDate(0, 0, -7).UnixMilli() / 86400000 * 86400000
	rows, err := db.Query(`SELECT qname, verdict, SUM(n) FROM roll_day_client WHERE bucket >= ? GROUP BY qname, verdict`, start)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type acc struct{ allowed, blocked, other int }
	by := map[string]*acc{}
	otherNames := map[string]int{}
	trackAllowed := map[string]int{}
	total, known := 0, 0
	for rows.Next() {
		var q, v string
		var n int
		if err := rows.Scan(&q, &v, &n); err != nil {
			t.Fatal(err)
		}
		total += n
		tr, ok := Lookup(q)
		if !ok {
			continue
		}
		known += n
		a := by[tr.CompanyID]
		if a == nil {
			a = &acc{}
			by[tr.CompanyID] = a
		}
		switch {
		case !tr.Tracking:
			a.other += n
			otherNames[q+" ("+tr.Category+")"] += n
		case v == "blocked":
			a.blocked += n
		default:
			a.allowed += n
			trackAllowed[q] += n
		}
	}
	fmt.Printf("lookups %d, with a known company %d\n", total, known)
	ids := make([]string, 0, len(by))
	for id := range by {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return by[ids[i]].allowed+by[ids[i]].blocked > by[ids[j]].allowed+by[ids[j]].blocked
	})
	fmt.Println("company: tracking allowed / tracking blocked / other")
	for i, id := range ids {
		if i == 25 {
			break
		}
		a := by[id]
		fmt.Printf("  %-34s %8d %8d %8d\n", CompanyName(id), a.allowed, a.blocked, a.other)
	}
	top := func(m map[string]int, n int, title string) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
		fmt.Println(title)
		for i, k := range keys {
			if i == n {
				break
			}
			fmt.Printf("  %8d  %s\n", m[k], k)
		}
	}
	top(trackAllowed, 15, "tracking names that got through:")
	top(otherNames, 15, "busiest non-tracking names of tracking companies:")
}
