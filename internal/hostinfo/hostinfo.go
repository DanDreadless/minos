// Package hostinfo reports what machine Minos is running on and how hard
// it is working: hostname and hardware once, then CPU/memory/disk/load
// sampled on a ticker.
//
// Three rules shape everything here.
//
// It is a leaf package. It imports nothing from Minos, so the dependency
// direction in CLAUDE.md holds and the per-OS files stay testable in
// isolation.
//
// Every dynamic reading is optional. A metric no platform hook can supply
// is nil, and the UI renders "—". A host-health panel must never fail a
// request, or fabricate a zero, because one number was unavailable —
// a confident 0% CPU is worse than an honest blank.
//
// Sampling happens on a ticker, never in the HTTP handler. CPU utilisation
// is a delta between two readings, so serving it on demand would mean
// sleeping inside a request. The newest Sample lives in an atomic pointer
// and the handler does one load.
package hostinfo

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"
)

// DefaultInterval is how often the sampler takes a reading. Short enough
// that the dashboard feels live, long enough that the CPU delta covers a
// meaningful span and the sampler itself is invisible in the numbers.
const DefaultInterval = 10 * time.Second

// MemorySource says what MemTotal is measuring. Inside a container the
// host's RAM is the wrong answer — someone who capped Minos at 256 MB
// should not be shown the host's 32 GB — so the UI needs to know which
// number it has.
const (
	MemoryHost   = "host"
	MemoryCgroup = "cgroup"
)

// Info is the static picture, read once at construction. Nothing here
// changes while the process runs.
type Info struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPUs      int    `json:"cpus"`
	Kernel    string `json:"kernel,omitempty"`
	Platform  string `json:"platform,omitempty"`
	GoVersion string `json:"go_version"`
	// Container reports that Minos is running in one. It changes how the
	// other numbers should be read, so it is displayed, not just used.
	Container bool `json:"container"`
	// MemTotal is 0 when unknown. MemSource names what it measured.
	MemTotal  uint64 `json:"mem_total,omitempty"`
	MemSource string `json:"mem_source,omitempty"`
}

// Sample is one reading. Pointer fields are absent when this platform
// cannot supply them — see the package comment; do not substitute zero.
type Sample struct {
	Time time.Time `json:"time"`

	// CPUPercent is utilisation across all cores since the previous
	// sample, so it is nil on the very first one: there is no delta yet.
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	// Load is the 1/5/15-minute run queue. Unix-only as a concept;
	// Windows has no equivalent and reports nil rather than a fake.
	Load1  *float64 `json:"load1,omitempty"`
	Load5  *float64 `json:"load5,omitempty"`
	Load15 *float64 `json:"load15,omitempty"`

	MemUsed      *uint64 `json:"mem_used,omitempty"`
	MemAvailable *uint64 `json:"mem_available,omitempty"`

	// Disk covers the filesystem holding the data directory — where the
	// query log lives. On a Pi that is the SD card, which is the number
	// most likely to actually ruin someone's week.
	DiskTotal *uint64 `json:"disk_total,omitempty"`
	DiskFree  *uint64 `json:"disk_free,omitempty"`

	// TempCelsius is the CPU package temperature where the platform
	// exposes one (Raspberry Pi does).
	TempCelsius *float64 `json:"temp_celsius,omitempty"`

	// UptimeSeconds is the host's, not the process's — the process's own
	// uptime is already on /api/status.
	UptimeSeconds *int64 `json:"uptime_seconds,omitempty"`

	// ProcRSS is Minos's resident set. Always available in some form:
	// where the OS cannot be asked, the Go runtime's own accounting is
	// used, which undercounts but is never absent.
	ProcRSS    *uint64 `json:"proc_rss,omitempty"`
	Goroutines int     `json:"goroutines"`
}

// Collector owns the static info and the newest sample.
type Collector struct {
	info     Info
	dataDir  string
	interval time.Duration
	sample   atomic.Pointer[Sample]

	// prev is the previous CPU reading, owned solely by the sampling
	// goroutine (and by New before it starts), so it needs no lock.
	prev   cpuTimes
	prevOK bool
}

// cpuTimes is one raw CPU accounting reading; utilisation is the ratio of
// the busy delta to the total delta between two of them.
type cpuTimes struct {
	busy, total uint64
}

// New reads the static info and takes an initial sample so a request
// arriving before the first tick still gets numbers. dataDir is the
// directory whose filesystem usage to report — the query log's home.
func New(dataDir string) *Collector {
	c := &Collector{
		dataDir:  dataDir,
		interval: DefaultInterval,
		info: Info{
			Hostname:  hostname(),
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			CPUs:      runtime.NumCPU(),
			GoVersion: runtime.Version(),
			Container: detectContainer(),
		},
	}
	c.info.Kernel, c.info.Platform = platformInfo()
	c.info.MemTotal, c.info.MemSource = memoryTotal()
	c.sample.Store(c.take())
	return c
}

// Info returns the static picture. Safe from any goroutine; never changes.
func (c *Collector) Info() Info { return c.info }

// Latest returns the newest sample. Never nil: New takes one.
func (c *Collector) Latest() *Sample { return c.sample.Load() }

// Run samples until ctx is cancelled. Call it in its own goroutine; it
// blocks. Cheap enough that the interval, not the work, sets the cost.
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.sample.Store(c.take())
		}
	}
}

// take reads every metric once. Called only from New and Run, so its
// access to c.prev is single-threaded.
func (c *Collector) take() *Sample {
	s := &Sample{Time: time.Now(), Goroutines: runtime.NumGoroutine()}

	if now, ok := readCPUTimes(); ok {
		if c.prevOK {
			s.CPUPercent = cpuPercent(c.prev, now)
		}
		c.prev, c.prevOK = now, true
	}

	if l1, l5, l15, ok := readLoad(); ok {
		s.Load1, s.Load5, s.Load15 = &l1, &l5, &l15
	}
	if used, avail, ok := memoryUsage(); ok {
		s.MemUsed, s.MemAvailable = &used, &avail
	}
	if total, free, ok := readDisk(c.dataDir); ok {
		s.DiskTotal, s.DiskFree = &total, &free
	}
	if temp, ok := readTemperature(); ok {
		s.TempCelsius = &temp
	}
	if up, ok := readUptime(); ok {
		secs := int64(up.Seconds())
		s.UptimeSeconds = &secs
	}
	s.ProcRSS = readProcessRSS()
	return s
}

// cpuPercent is utilisation between two readings, or nil when the pair
// cannot support one. Utilisation needs two samples, so the first has no
// predecessor; and a counter that stalled or went backwards — a container
// migrated, a counter wrapped, a virtualised clock stepped — is not a
// measurement. Reporting a spike, or dividing by a zero delta, would be
// worse than reporting nothing.
func cpuPercent(prev, now cpuTimes) *float64 {
	if now.total <= prev.total || now.busy < prev.busy {
		return nil
	}
	busy := float64(now.busy - prev.busy)
	total := float64(now.total - prev.total)
	pct := busy / total * 100
	if pct < 0 || pct > 100 {
		return nil
	}
	return &pct
}

// runtimeRSS is the fallback resident-set figure: Go's own heap
// accounting. It undercounts (it cannot see the binary, stacks, or
// anything cgo mapped), so platforms that can ask the OS should.
func runtimeRSS() *uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	v := m.Sys - m.HeapReleased
	return &v
}
