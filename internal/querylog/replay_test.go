//go:build replay

package querylog

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestReplayDashboard times the dashboard's rollup reads on a real query
// log, read-only, and counts what a daily tier would read instead. Prints
// timings and row counts only.
//
//	MINOS_DB=/var/lib/minos/minos.db go test -tags replay -run TestReplayDashboard ./internal/querylog
func TestReplayDashboard(t *testing.T) {
	path := os.Getenv("MINOS_DB")
	if path == "" {
		t.Skip("MINOS_DB not set")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, days := range []int{7, 30, 90} {
		full := alignUp(time.Now().AddDate(0, 0, -days).UnixMilli(), hourMs)
		fmt.Printf("== %d days\n", days)
		for _, q := range []struct{ name, sql string }{
			{"rows roll_hour", `SELECT COUNT(*) FROM roll_hour WHERE bucket >= ?`},
			{"rows roll_hour by day", `SELECT COUNT(*) FROM (SELECT DISTINCT bucket/86400000, client, verdict, list FROM roll_hour WHERE bucket >= ?)`},
			{"rows roll_hour_blocked", `SELECT COUNT(*) FROM roll_hour_blocked WHERE bucket >= ?`},
			{"rows roll_hour_blocked by day", `SELECT COUNT(*) FROM (SELECT DISTINCT bucket/86400000, qname FROM roll_hour_blocked WHERE bucket >= ?)`},
		} {
			var n int
			start := time.Now()
			if err := db.QueryRow(q.sql, full).Scan(&n); err != nil {
				t.Fatal(err)
			}
			fmt.Printf("  %-32s %9d  (%s)\n", q.name, n, time.Since(start).Round(time.Millisecond))
		}
		for _, q := range []struct{ name, sql string }{
			{"timeline (roll_hour)", `SELECT bucket, verdict, SUM(n) FROM roll_hour WHERE bucket >= ? GROUP BY bucket, verdict`},
			{"top clients (roll_hour)", `SELECT client, verdict, list, SUM(n) FROM roll_hour WHERE bucket >= ? GROUP BY client, verdict, list`},
			{"top blocked (roll_hour_blocked)", `SELECT qname, SUM(n) FROM roll_hour_blocked WHERE bucket >= ? GROUP BY qname`},
		} {
			start := time.Now()
			rows, err := db.Query(q.sql, full)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for rows.Next() {
				n++
			}
			rows.Close()
			fmt.Printf("  %-32s %9d groups in %s\n", q.name, n, time.Since(start).Round(time.Millisecond))
		}
		// The same answers, summed in Go from a plain range read.
		start := time.Now()
		rows, err := db.Query(`SELECT bucket, client, verdict, list, n FROM roll_hour WHERE bucket >= ?`, full)
		if err != nil {
			t.Fatal(err)
		}
		type ck struct{ c, v, l string }
		type tk struct {
			day int64
			v   string
		}
		timeline, clients := map[tk]int{}, map[ck]int{}
		read := 0
		for rows.Next() {
			var b int64
			var c, v, l string
			var n int
			if err := rows.Scan(&b, &c, &v, &l, &n); err != nil {
				t.Fatal(err)
			}
			timeline[tk{b / dayMs, v}] += n
			clients[ck{c, v, l}] += n
			read++
		}
		rows.Close()
		fmt.Printf("  %-32s %9d rows -> %d + %d groups in %s\n", "timeline + clients, Go", read, len(timeline), len(clients), time.Since(start).Round(time.Millisecond))
		start = time.Now()
		rows, err = db.Query(`SELECT qname, n FROM roll_hour_blocked WHERE bucket >= ?`, full)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]int{}
		read = 0
		for rows.Next() {
			var q string
			var n int
			if err := rows.Scan(&q, &n); err != nil {
				t.Fatal(err)
			}
			names[q] += n
			read++
		}
		rows.Close()
		fmt.Printf("  %-32s %9d rows -> %d groups in %s\n", "top blocked, Go", read, len(names), time.Since(start).Round(time.Millisecond))
	}
}

// TestReplayDailyTiers builds the daily tiers as TEMP tables on a read-only
// connection to a real log and times the dashboard's reads through them.
func TestReplayDailyTiers(t *testing.T) {
	path := os.Getenv("MINOS_DB")
	if path == "" {
		t.Skip("MINOS_DB not set")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // TEMP tables live on one connection
	start := time.Now()
	for _, stmt := range []string{
		`CREATE TEMP TABLE roll_day (bucket INTEGER NOT NULL, client TEXT NOT NULL, verdict TEXT NOT NULL, list TEXT NOT NULL,
			n INTEGER NOT NULL, PRIMARY KEY (bucket, client, verdict, list)) WITHOUT ROWID`,
		`INSERT INTO roll_day SELECT bucket / 86400000 * 86400000, client, verdict, list, SUM(n) FROM main.roll_hour GROUP BY 1, client, verdict, list`,
		`CREATE TEMP TABLE roll_day_blocked (bucket INTEGER NOT NULL, qname TEXT NOT NULL, n INTEGER NOT NULL,
			PRIMARY KEY (bucket, qname)) WITHOUT ROWID`,
		`INSERT INTO roll_day_blocked SELECT bucket / 86400000 * 86400000, qname, SUM(n) FROM main.roll_hour_blocked GROUP BY 1, qname`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Printf("seed (what an upgrade does once): %s\n", time.Since(start).Round(time.Millisecond))
	l := &Log{}
	l.rdb.Store(db)
	ctx := t.Context()
	for _, days := range []int{7, 30, 90} {
		since := time.Now().AddDate(0, 0, -days)
		full := alignUp(since.UnixMilli(), hourMs)
		start := time.Now()
		n := 0
		_ = l.tiered(ctx, full, "roll_hour", "roll_day",
			`SELECT bucket, verdict, SUM(n) FROM %s WHERE bucket >= ? AND bucket < ? GROUP BY bucket, verdict`,
			func(*sql.Rows) error { n++; return nil })
		tl := time.Since(start)
		start = time.Now()
		_ = l.tiered(ctx, full, "roll_hour", "roll_day",
			`SELECT client, verdict, list, SUM(n) FROM %s WHERE bucket >= ? AND bucket < ? GROUP BY client, verdict, list`,
			func(*sql.Rows) error { n++; return nil })
		cl := time.Since(start)
		start = time.Now()
		_ = l.tiered(ctx, full, "roll_hour_blocked", "roll_day_blocked",
			`SELECT qname, SUM(n) FROM %s WHERE bucket >= ? AND bucket < ? GROUP BY qname`,
			func(*sql.Rows) error { n++; return nil })
		bl := time.Since(start)
		fmt.Printf("%2d days: timeline %s, top devices %s, top blocked %s — total %s\n", days,
			tl.Round(time.Millisecond), cl.Round(time.Millisecond), bl.Round(time.Millisecond), (tl + cl + bl).Round(time.Millisecond))
	}
}
