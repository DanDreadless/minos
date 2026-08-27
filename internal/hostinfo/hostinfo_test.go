package hostinfo

import (
	"context"
	"testing"
	"time"
)

// New must always yield usable static Info, and on a supported host a
// first Sample too — a request arriving before the first tick would
// otherwise see nothing. This runs on whatever platform CI is on,
// including inside a container, so it branches on Supported rather than
// assuming.
func TestNewAlwaysProducesInfoAndSample(t *testing.T) {
	c := New(t.TempDir())

	info := c.Info()
	if info.OS == "" || info.Arch == "" || info.GoVersion == "" {
		t.Errorf("static info incomplete: %+v", info)
	}
	if info.CPUs < 1 {
		t.Errorf("cpus = %d, want at least 1", info.CPUs)
	}

	if !c.Supported() {
		if c.Latest() != nil {
			t.Error("Latest() is non-nil in a container, want nil — host health is not reported there")
		}
		t.Skip("container host: nothing further to assert")
	}

	s := c.Latest()
	if s == nil {
		t.Fatal("Latest() is nil after New — a request before the first tick would see nothing")
	}
	if s.Time.IsZero() {
		t.Error("sample has no timestamp")
	}
	if s.Goroutines < 1 {
		t.Errorf("goroutines = %d, want at least 1", s.Goroutines)
	}
	// Available on every platform: the runtime fallback guarantees it.
	if s.ProcRSS == nil {
		t.Error("proc_rss is nil, want at least the runtime's own accounting")
	}
}

// CPU utilisation is a delta, so the first sample cannot have one. A zero
// would read as "idle" rather than "not known yet".
func TestFirstSampleHasNoCPUPercent(t *testing.T) {
	c := New(t.TempDir())
	if !c.Supported() {
		t.Skip("container host: no samples taken")
	}
	if s := c.Latest(); s.CPUPercent != nil {
		t.Errorf("cpu_percent = %v on the first sample, want nil — there is no delta yet", *s.CPUPercent)
	}
}

func TestRunSamplesUntilCancelled(t *testing.T) {
	c := New(t.TempDir())
	if !c.Supported() {
		t.Skip("container host: the sampler does not run")
	}
	c.interval = 5 * time.Millisecond
	first := c.Latest()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for c.Latest() == first {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("sampler produced no new sample")
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// A container cannot answer "how is the machine running Minos doing?" —
// the host's figures describe a machine Minos does not have to itself,
// and the container's budget is not the host. So it reports nothing, and
// the sampler must not run or leak a goroutine pretending otherwise.
func TestContainerReportsNothing(t *testing.T) {
	c := New(t.TempDir())
	c.info.Container = true
	c.sample.Store(nil)

	if c.Supported() {
		t.Fatal("Supported() is true with Container set")
	}
	if c.Latest() != nil {
		t.Error("Latest() is non-nil on an unsupported host")
	}

	done := make(chan struct{})
	go func() { defer close(done); c.Run(t.Context()) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return immediately on an unsupported host")
	}
	if c.Latest() != nil {
		t.Error("Run produced a sample on an unsupported host")
	}
}

// A backwards or stalled CPU counter is not a measurement. Reporting a
// spike (or dividing by zero) because a container migrated or a counter
// wrapped would be worse than reporting nothing.
func TestCPUDeltaRejectsNonMonotonicCounters(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prev, next cpuTimes
		want       bool // a percentage should be produced
	}{
		{"normal forward delta", cpuTimes{busy: 100, total: 1000}, cpuTimes{busy: 150, total: 1100}, true},
		{"total went backwards", cpuTimes{busy: 100, total: 1000}, cpuTimes{busy: 150, total: 900}, false},
		{"busy went backwards", cpuTimes{busy: 100, total: 1000}, cpuTimes{busy: 50, total: 1100}, false},
		{"total unchanged", cpuTimes{busy: 100, total: 1000}, cpuTimes{busy: 100, total: 1000}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cpuPercent(tc.prev, tc.next)
			if (got != nil) != tc.want {
				t.Fatalf("cpuPercent(%+v, %+v) = %v, want produced=%v", tc.prev, tc.next, got, tc.want)
			}
			if got != nil && (*got < 0 || *got > 100) {
				t.Errorf("cpu_percent = %v, want within 0-100", *got)
			}
		})
	}
}
