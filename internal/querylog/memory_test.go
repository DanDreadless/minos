package querylog

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func pragmaInt(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

// The cap is real SQLite configuration read back from the engine on both
// pools — not just a number in our config — and it changes live.
func TestMemoryCapIsAppliedAndChangesLive(t *testing.T) {
	l, err := Open(Options{RingSize: 10, DBPath: filepath.Join(t.TempDir(), "q.db"), RetentionDays: 1, MemoryMB: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// 40 MB over 5 connections = 8 MB each, as a negative KiB count.
	for name, db := range map[string]*sql.DB{"writer": l.db, "reader": l.reader()} {
		if got := pragmaInt(t, db, "cache_size"); got != -8192 {
			t.Errorf("%s cache_size = %d, want -8192", name, got)
		}
		// Work bigger than the cache spills to a file, never to RAM.
		if got := pragmaInt(t, db, "temp_store"); got != 1 {
			t.Errorf("%s temp_store = %d, want 1 (FILE)", name, got)
		}
		if got := pragmaInt(t, db, "mmap_size"); got != 0 {
			t.Errorf("%s mmap_size = %d, want 0", name, got)
		}
	}

	l.SetMemoryMB(80)
	if got := pragmaInt(t, l.reader(), "cache_size"); got != -16384 {
		t.Errorf("after the change, reader cache_size = %d, want -16384 (new pool)", got)
	}
	if got := pragmaInt(t, l.db, "cache_size"); got != -16384 {
		t.Errorf("after the change, writer cache_size = %d, want -16384", got)
	}
	// Close must release the replaced pool too, not leave it to its timer
	// (on Windows an open handle even blocks deleting the file).
}

func TestDefaultMemoryCap(t *testing.T) {
	l, err := Open(Options{RingSize: 10, DBPath: filepath.Join(t.TempDir(), "q.db"), RetentionDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got, want := pragmaInt(t, l.reader(), "cache_size"), int64(-cacheKiB(DefaultMemoryMB)); got != want {
		t.Errorf("default cache_size = %d, want %d", got, want)
	}
}
