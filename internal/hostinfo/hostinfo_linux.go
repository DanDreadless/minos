//go:build linux

package hostinfo

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Linux reads everything from /proc and /sys — no cgo, no external
// binaries, which is what the scratch container and CGO_ENABLED=0 builds
// require. Parsing is split from reading so the per-file parsers are
// tested against fixtures rather than whatever this machine reports.

// cgroup v2 paths. Inside a container these describe the limit Minos was
// given; /proc/meminfo describes the host and is the wrong answer there.
// Someone who capped Minos at 256 MB must not be shown 32 GB.
const (
	cgroupMemMax     = "/sys/fs/cgroup/memory.max"
	cgroupMemCurrent = "/sys/fs/cgroup/memory.current"
)

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// detectContainer looks for the same markers the update checker uses.
// Deliberately duplicated rather than imported: hostinfo is a leaf
// package and must not depend on internal/api.
func detectContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") ||
			strings.Contains(s, "kubepods") || strings.Contains(s, "lxc") {
			return true
		}
	}
	// A cgroup v2 container has no cgroup names to match on, but it does
	// have a memory ceiling where the host has none.
	if b, err := os.ReadFile(cgroupMemMax); err == nil {
		if _, ok := parseCgroupLimit(b); ok {
			return true
		}
	}
	return false
}

func platformInfo() (kernel, platform string) {
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		platform = parseOSRelease(b)
	}
	return kernel, platform
}

// parseOSRelease pulls PRETTY_NAME out of /etc/os-release.
func parseOSRelease(b []byte) string {
	for line := range strings.SplitSeq(string(b), "\n") {
		name, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || name != "PRETTY_NAME" {
			continue
		}
		return strings.Trim(val, "\"")
	}
	return ""
}

// memoryTotal prefers a cgroup ceiling over the host's RAM, so a
// container reports the budget it was actually given.
func memoryTotal() (uint64, string) {
	if b, err := os.ReadFile(cgroupMemMax); err == nil {
		if limit, ok := parseCgroupLimit(b); ok {
			return limit, MemoryCgroup
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		if total, _, ok := parseMeminfo(b); ok {
			return total, MemoryHost
		}
	}
	return 0, ""
}

// memoryUsage returns used and available bytes, matched to whatever
// memoryTotal measured — mixing a cgroup total with host usage would
// produce a percentage that is not about anything.
func memoryUsage() (used, avail uint64, ok bool) {
	if limitB, err := os.ReadFile(cgroupMemMax); err == nil {
		if limit, lok := parseCgroupLimit(limitB); lok {
			if curB, err := os.ReadFile(cgroupMemCurrent); err == nil {
				if cur, cok := parseUint(curB); cok && cur <= limit {
					return cur, limit - cur, true
				}
			}
		}
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	total, available, pok := parseMeminfo(b)
	if !pok || available > total {
		return 0, 0, false
	}
	return total - available, available, true
}

// parseCgroupLimit reads a cgroup v2 limit file. The literal "max" means
// no limit is set, which is not a memory size — the caller must fall back
// to the host rather than treat it as a number.
func parseCgroupLimit(b []byte) (uint64, bool) {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	// Unlimited cgroups have historically reported a huge sentinel rather
	// than "max". Anything past a petabyte is that, not real RAM.
	if v > 1<<50 {
		return 0, false
	}
	return v, true
}

func parseUint(b []byte) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return v, err == nil
}

// parseMeminfo returns total and available bytes from /proc/meminfo.
// MemAvailable is the kernel's own estimate of what a new workload could
// get; MemFree is not the same thing and is far too pessimistic, since it
// ignores reclaimable page cache.
func parseMeminfo(b []byte) (total, available uint64, ok bool) {
	var haveTotal, haveAvail bool
	for line := range strings.SplitSeq(string(b), "\n") {
		key, rest, cut := strings.Cut(line, ":")
		if !cut {
			continue
		}
		switch key {
		case "MemTotal", "MemAvailable":
		default:
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		v *= 1024 // /proc/meminfo is in kB
		if key == "MemTotal" {
			total, haveTotal = v, true
		} else {
			available, haveAvail = v, true
		}
	}
	return total, available, haveTotal && haveAvail
}

func readLoad() (l1, l5, l15 float64, ok bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, false
	}
	return parseLoadavg(b)
}

func parseLoadavg(b []byte) (l1, l5, l15 float64, ok bool) {
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, 0, 0, false
	}
	var err error
	if l1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return 0, 0, 0, false
	}
	if l5, err = strconv.ParseFloat(f[1], 64); err != nil {
		return 0, 0, 0, false
	}
	if l15, err = strconv.ParseFloat(f[2], 64); err != nil {
		return 0, 0, 0, false
	}
	return l1, l5, l15, true
}

func readCPUTimes() (cpuTimes, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}
	return parseStatCPU(b)
}

// parseStatCPU sums the aggregate "cpu" line. Idle and iowait are the
// not-busy portion; everything else counts as work.
func parseStatCPU(b []byte) (cpuTimes, bool) {
	for line := range strings.SplitSeq(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return cpuTimes{}, false
		}
		var t cpuTimes
		for i, f := range fields {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return cpuTimes{}, false
			}
			t.total += v
			// Fields are user, nice, system, idle, iowait, …
			if i != 3 && i != 4 {
				t.busy += v
			}
		}
		return t, true
	}
	return cpuTimes{}, false
}

func readUptime() (time.Duration, bool) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	return parseUptime(b)
}

func parseUptime(b []byte) (time.Duration, bool) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(f[0], 64)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

// thermalZone0 is where the Raspberry Pi exposes its SoC temperature. On
// other hardware zone 0 may be something else entirely, so an
// implausible reading is discarded rather than shown.
const thermalZone0 = "/sys/class/thermal/thermal_zone0/temp"

func readTemperature() (float64, bool) {
	b, err := os.ReadFile(thermalZone0)
	if err != nil {
		return 0, false
	}
	return parseThermal(b)
}

// parseThermal converts a millidegree reading. Some sensors report
// degrees directly, so the magnitude decides the scale; anything outside
// a range a running computer could plausibly be is rejected.
func parseThermal(b []byte) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, false
	}
	c := v / 1000
	if c < 1 || c > 150 {
		// Not millidegrees: try the raw value before giving up.
		c = v
	}
	if c < -50 || c > 150 {
		return 0, false
	}
	return c, true
}

func readDisk(path string) (total, free uint64, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bs := uint64(st.Bsize)
	// Bavail, not Bfree: the blocks reserved for root are not space a
	// non-root Minos can actually use.
	return st.Blocks * bs, st.Bavail * bs, true
}

// readProcessRSS asks the kernel for the real resident set, which
// includes the binary and stacks Go's own accounting cannot see.
func readProcessRSS() *uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return runtimeRSS()
	}
	if rss, ok := parseStatm(b, uint64(os.Getpagesize())); ok {
		return &rss
	}
	return runtimeRSS()
}

// parseStatm reads the resident field (second) of /proc/self/statm,
// which counts pages.
func parseStatm(b []byte, pageSize uint64) (uint64, bool) {
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * pageSize, true
}
