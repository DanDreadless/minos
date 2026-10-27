package querylog

import (
	"database/sql"
	"fmt"
)

// Rollup grains. Buckets are unix-millisecond starts aligned exactly as the
// raw Timeline always aligned them (ts / width * width), so a chart drawn
// from rollups is bucket-for-bucket the chart drawn from the log.
const (
	tenMinMs = int64(10 * 60 * 1000)
	hourMs   = int64(60 * 60 * 1000)
	dayMs    = int64(24 * 60 * 60 * 1000)

	// roll10mKeepMs: the 10-minute series only ever charts the last 24 h.
	roll10mKeepMs = 2 * dayMs
)

const rollupSchema = `
CREATE TABLE IF NOT EXISTS roll_10m (
	bucket  INTEGER NOT NULL,
	verdict TEXT NOT NULL,
	n       INTEGER NOT NULL,
	PRIMARY KEY (bucket, verdict)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS roll_hour (
	bucket  INTEGER NOT NULL,
	client  TEXT NOT NULL,
	verdict TEXT NOT NULL,
	list    TEXT NOT NULL,
	n       INTEGER NOT NULL,
	PRIMARY KEY (bucket, client, verdict, list)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS roll_hour_blocked (
	bucket INTEGER NOT NULL,
	qname  TEXT NOT NULL,
	n      INTEGER NOT NULL,
	PRIMARY KEY (bucket, qname)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS roll_day (
	bucket  INTEGER NOT NULL,
	client  TEXT NOT NULL,
	verdict TEXT NOT NULL,
	list    TEXT NOT NULL,
	n       INTEGER NOT NULL,
	PRIMARY KEY (bucket, client, verdict, list)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS roll_day_blocked (
	bucket INTEGER NOT NULL,
	qname  TEXT NOT NULL,
	n      INTEGER NOT NULL,
	PRIMARY KEY (bucket, qname)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS roll_day_client (
	client  TEXT NOT NULL,
	bucket  INTEGER NOT NULL,
	verdict TEXT NOT NULL,
	qname   TEXT NOT NULL,
	n       INTEGER NOT NULL,
	PRIMARY KEY (client, bucket, verdict, qname)
) WITHOUT ROWID;`

// derivedTables are dropped and rebuilt when derivedVersion changes.
// roll_day and roll_day_blocked are roll_hour and roll_hour_blocked by UTC
// day: a 90-day dashboard read 162,000 hourly rows (1.8 s on a Pi 4) and
// reads ~34,000 daily ones instead.
var derivedTables = []string{"qnames", "roll_10m", "roll_hour", "roll_hour_blocked", "roll_day_client",
	"roll_day", "roll_day_blocked"}

type (
	tenKey struct {
		bucket  int64
		verdict string
	}
	hourKey struct {
		bucket                int64
		client, verdict, list string
	}
	hourBlockKey struct {
		bucket int64
		qname  string
	}
	dayClientKey struct {
		client  string
		bucket  int64
		verdict string
		qname   string
	}
)

// derivedAgg folds rows into every derived table's increments. Live batches
// and backfill chunks both go through it, so the two can never compute a
// different answer for the same rows.
type derivedAgg struct {
	names     map[string]*nameAgg
	tenMin    map[tenKey]int64
	hour      map[hourKey]int64
	hourBlock map[hourBlockKey]int64
	dayClient map[dayClientKey]int64
	day       map[hourKey]int64      // roll_day: hourKey with a day bucket
	dayBlock  map[hourBlockKey]int64 // roll_day_blocked
}

func newDerivedAgg() *derivedAgg {
	return &derivedAgg{
		names:     make(map[string]*nameAgg),
		tenMin:    make(map[tenKey]int64),
		hour:      make(map[hourKey]int64),
		hourBlock: make(map[hourBlockKey]int64),
		dayClient: make(map[dayClientKey]int64),
		day:       make(map[hourKey]int64),
		dayBlock:  make(map[hourBlockKey]int64),
	}
}

func (a *derivedAgg) add(ts int64, client, qname, verdict, list string) {
	na := a.names[qname]
	if na == nil {
		na = &nameAgg{}
		a.names[qname] = na
	}
	na.hits++
	if verdict == VerdictBlocked {
		na.blocked++
	}
	if ts > na.lastTs {
		na.lastTs = ts
	}
	a.tenMin[tenKey{ts / tenMinMs * tenMinMs, verdict}]++
	hour := ts / hourMs * hourMs
	day := ts / dayMs * dayMs
	a.hour[hourKey{hour, client, verdict, list}]++
	a.day[hourKey{day, client, verdict, list}]++
	if verdict == VerdictBlocked {
		a.hourBlock[hourBlockKey{hour, qname}]++
		a.dayBlock[hourBlockKey{day, qname}]++
	}
	a.dayClient[dayClientKey{client, day, verdict, qname}]++
}

// upsert adds the increments to the derived tables inside tx.
func (a *derivedAgg) upsert(tx *sql.Tx) error {
	if err := upsertNames(tx, a.names); err != nil {
		return err
	}
	type job struct {
		sql  string
		rows func(exec func(args ...any) error) error
	}
	jobs := []job{
		{`INSERT INTO roll_10m (bucket, verdict, n) VALUES (?, ?, ?)
			ON CONFLICT(bucket, verdict) DO UPDATE SET n = n + excluded.n`,
			func(exec func(...any) error) error {
				for k, n := range a.tenMin {
					if err := exec(k.bucket, k.verdict, n); err != nil {
						return err
					}
				}
				return nil
			}},
		{`INSERT INTO roll_hour (bucket, client, verdict, list, n) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(bucket, client, verdict, list) DO UPDATE SET n = n + excluded.n`,
			func(exec func(...any) error) error {
				for k, n := range a.hour {
					if err := exec(k.bucket, k.client, k.verdict, k.list, n); err != nil {
						return err
					}
				}
				return nil
			}},
		{`INSERT INTO roll_hour_blocked (bucket, qname, n) VALUES (?, ?, ?)
			ON CONFLICT(bucket, qname) DO UPDATE SET n = n + excluded.n`,
			func(exec func(...any) error) error {
				for k, n := range a.hourBlock {
					if err := exec(k.bucket, k.qname, n); err != nil {
						return err
					}
				}
				return nil
			}},
		{`INSERT INTO roll_day_client (client, bucket, verdict, qname, n) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(client, bucket, verdict, qname) DO UPDATE SET n = n + excluded.n`,
			func(exec func(...any) error) error {
				for k, n := range a.dayClient {
					if err := exec(k.client, k.bucket, k.verdict, k.qname, n); err != nil {
						return err
					}
				}
				return nil
			}},
		{`INSERT INTO roll_day (bucket, client, verdict, list, n) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(bucket, client, verdict, list) DO UPDATE SET n = n + excluded.n`,
			func(exec func(...any) error) error {
				for k, n := range a.day {
					if err := exec(k.bucket, k.client, k.verdict, k.list, n); err != nil {
						return err
					}
				}
				return nil
			}},
		{`INSERT INTO roll_day_blocked (bucket, qname, n) VALUES (?, ?, ?)
			ON CONFLICT(bucket, qname) DO UPDATE SET n = n + excluded.n`,
			func(exec func(...any) error) error {
				for k, n := range a.dayBlock {
					if err := exec(k.bucket, k.qname, n); err != nil {
						return err
					}
				}
				return nil
			}},
	}
	for _, j := range jobs {
		stmt, err := tx.Prepare(j.sql)
		if err != nil {
			return fmt.Errorf("prepare rollup upsert: %w", err)
		}
		err = j.rows(func(args ...any) error {
			_, err := stmt.Exec(args...)
			return err
		})
		stmt.Close()
		if err != nil {
			return fmt.Errorf("rollup upsert: %w", err)
		}
	}
	return nil
}
