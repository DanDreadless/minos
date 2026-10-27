//go:build replay

package lists

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"minos/internal/filter"
)

// TestReplayList checks what a candidate blocklist would have blocked on a
// real network in the last week, before it goes in the catalog. Read-only;
// prints names and counts only, never devices.
//
//	MINOS_DB=/var/lib/minos/minos.db LIST=dga7.txt FORMAT=adblock go test -tags replay -run TestReplayList ./internal/lists
func TestReplayList(t *testing.T) {
	path, list := os.Getenv("MINOS_DB"), os.Getenv("LIST")
	if path == "" || list == "" {
		t.Skip("MINOS_DB and LIST not set")
	}
	body, err := os.Open(list)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	b := filter.NewBuilder()
	stats, err := Parse(os.Getenv("FORMAT"), "candidate", false, body, b)
	if err != nil {
		t.Fatal(err)
	}
	m := b.Build()
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
	total, hit, hitAllowed := 0, 0, 0
	names := map[string]int{}
	for rows.Next() {
		var q, v string
		var n int
		if err := rows.Scan(&q, &v, &n); err != nil {
			t.Fatal(err)
		}
		total += n
		if r := m.Match(q); r.Blocked {
			hit += n
			if v != "blocked" {
				hitAllowed += n
				names[q+" ("+r.Rule+")"] += n
			}
		}
	}
	fmt.Printf("list: %d rules, %d skipped\n", stats.Rules, stats.Skipped)
	fmt.Printf("last 7 days: %d lookups; the list matches %d, of which %d were allowed by your current lists\n", total, hit, hitAllowed)
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return names[keys[i]] > names[keys[j]] })
	for i, k := range keys {
		if i == 40 {
			fmt.Printf("  … %d more names\n", len(keys)-40)
			break
		}
		fmt.Printf("  %6d  %s\n", names[k], strings.TrimSpace(k))
	}
}
