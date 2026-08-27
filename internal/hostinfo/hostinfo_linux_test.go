//go:build linux

package hostinfo

import "testing"

// Fixtures, not the live machine: a test that reads this host's /proc
// asserts nothing repeatable and passes for the wrong reasons in CI.

func TestParseMeminfo(t *testing.T) {
	const meminfo = `MemTotal:        8039084 kB
MemFree:          213456 kB
MemAvailable:    4127732 kB
Buffers:          102400 kB
Cached:          3200000 kB
`
	total, avail, ok := parseMeminfo([]byte(meminfo))
	if !ok {
		t.Fatal("parse failed on well-formed meminfo")
	}
	if want := uint64(8039084) * 1024; total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	// MemAvailable, not MemFree: free ignores reclaimable page cache and
	// would report this host as nearly out of memory when it is not.
	if want := uint64(4127732) * 1024; avail != want {
		t.Errorf("available = %d, want %d (MemAvailable, not MemFree)", avail, want)
	}
}

func TestParseMeminfoRejectsIncomplete(t *testing.T) {
	// A kernel too old for MemAvailable: decline rather than silently
	// substitute MemFree, which means something different.
	const old = "MemTotal:        8039084 kB\nMemFree:          213456 kB\n"
	if _, _, ok := parseMeminfo([]byte(old)); ok {
		t.Error("parsed meminfo without MemAvailable, want a decline")
	}
	if _, _, ok := parseMeminfo([]byte("garbage\n\n")); ok {
		t.Error("parsed garbage, want a decline")
	}
}

func TestParseStatCPU(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	const stat = `cpu  100 20 50 800 30 0 5 0 0 0
cpu0 50 10 25 400 15 0 2 0 0 0
intr 12345
`
	got, ok := parseStatCPU([]byte(stat))
	if !ok {
		t.Fatal("parse failed on well-formed /proc/stat")
	}
	// Everything except idle (800) and iowait (30) is work.
	if want := uint64(100 + 20 + 50 + 5); got.busy != want {
		t.Errorf("busy = %d, want %d (idle and iowait excluded)", got.busy, want)
	}
	if want := uint64(100 + 20 + 50 + 800 + 30 + 5); got.total != want {
		t.Errorf("total = %d, want %d", got.total, want)
	}
}

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15, ok := parseLoadavg([]byte("0.52 0.31 0.28 1/512 12345\n"))
	if !ok {
		t.Fatal("parse failed")
	}
	if l1 != 0.52 || l5 != 0.31 || l15 != 0.28 {
		t.Errorf("load = %v %v %v, want 0.52 0.31 0.28", l1, l5, l15)
	}
	if _, _, _, ok := parseLoadavg([]byte("0.52 0.31\n")); ok {
		t.Error("parsed a truncated loadavg, want a decline")
	}
}

func TestParseUptime(t *testing.T) {
	d, ok := parseUptime([]byte("12345.67 98765.43\n"))
	if !ok || int(d.Seconds()) != 12345 {
		t.Errorf("uptime = %v ok=%v, want ~12345s", d, ok)
	}
	if _, ok := parseUptime([]byte("not-a-number\n")); ok {
		t.Error("parsed garbage uptime, want a decline")
	}
}

// The cgroup limit decides whether a container shows its own budget or
// the host's RAM, so "no limit" must never be mistaken for a size.
func TestParseCgroupLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want uint64
		ok   bool
	}{
		{"a real limit", "268435456\n", 268435456, true},
		{"the literal max means unlimited", "max\n", 0, false},
		{"empty", "", 0, false},
		{"zero is not a budget", "0\n", 0, false},
		{"legacy unlimited sentinel", "9223372036854771712\n", 0, false},
		{"garbage", "banana\n", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCgroupLimit([]byte(tc.in))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseCgroupLimit(%q) = %d,%v want %d,%v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseThermal(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want float64
		ok   bool
	}{
		{"millidegrees, the usual form", "48678\n", 48.678, true},
		{"already degrees", "48\n", 48, true},
		{"implausible is rejected", "999999\n", 0, false},
		{"garbage", "warm\n", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseThermal([]byte(tc.in))
			if ok != tc.ok {
				t.Fatalf("parseThermal(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if ok && (got < tc.want-0.01 || got > tc.want+0.01) {
				t.Errorf("parseThermal(%q) = %v, want ~%v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseStatm(t *testing.T) {
	// size resident shared text lib data dt
	got, ok := parseStatm([]byte("12345 678 90 12 0 345 0\n"), 4096)
	if !ok || got != 678*4096 {
		t.Errorf("parseStatm = %d ok=%v, want %d", got, ok, 678*4096)
	}
	if _, ok := parseStatm([]byte("12345\n"), 4096); ok {
		t.Error("parsed statm without a resident field, want a decline")
	}
}

func TestParseOSRelease(t *testing.T) {
	const osr = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
`
	if got := parseOSRelease([]byte(osr)); got != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("parseOSRelease = %q", got)
	}
	if got := parseOSRelease([]byte("NAME=\"x\"\n")); got != "" {
		t.Errorf("parseOSRelease without PRETTY_NAME = %q, want empty", got)
	}
}
