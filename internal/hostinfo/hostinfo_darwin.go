//go:build darwin

package hostinfo

import (
	"encoding/binary"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Darwin reads what sysctl can answer without cgo. Two metrics are
// deliberately absent rather than approximated:
//
// CPU utilisation needs host_processor_info, a Mach call reachable only
// through cgo — and every release build is CGO_ENABLED=0. Load average
// is shown instead, which answers the same question ("is this machine
// busy?") with a number sysctl does provide.
//
// Memory *usage* needs host_statistics64 for the same reason. Total RAM
// is reported; used and available decline. macOS is a development
// target here — deployment is Linux, Pi and Docker — so closing these
// two holes is not worth a cgo dependency that would break every
// release build.

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// detectContainer is always false: macOS containers are Linux VMs, and
// the Minos process inside one reports GOOS=linux, not darwin.
func detectContainer() bool { return false }

func platformInfo() (kernel, platform string) {
	kernel, _ = unix.Sysctl("kern.osrelease")
	name, err := unix.Sysctl("kern.ostype")
	if err == nil && name != "" {
		if v, err := unix.Sysctl("kern.osproductversion"); err == nil && v != "" {
			platform = "macOS " + v
		} else {
			platform = name
		}
	}
	return kernel, platform
}

func memoryTotal() (uint64, string) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, ""
	}
	return total, MemoryHost
}

// memoryUsage declines: see the package note above. Reporting total with
// no breakdown is honest; inventing one is not.
func memoryUsage() (used, avail uint64, ok bool) { return 0, 0, false }

// readLoad parses the kernel's struct loadavg: three fixed-point values
// and the scale to divide them by.
func readLoad() (l1, l5, l15 float64, ok bool) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 16 {
		return 0, 0, 0, false
	}
	scale := float64(binary.LittleEndian.Uint32(raw[12:16]))
	if scale == 0 {
		return 0, 0, 0, false
	}
	v := func(off int) float64 {
		return float64(binary.LittleEndian.Uint32(raw[off:off+4])) / scale
	}
	return v(0), v(4), v(8), true
}

// readCPUTimes declines, so cpuPercent is never computed on darwin and
// the UI shows load average in its place.
func readCPUTimes() (cpuTimes, bool) { return cpuTimes{}, false }

func readUptime() (time.Duration, bool) {
	raw, err := unix.SysctlRaw("kern.boottime")
	if err != nil || len(raw) < 8 {
		return 0, false
	}
	boot := int64(binary.LittleEndian.Uint64(raw[0:8]))
	if boot <= 0 {
		return 0, false
	}
	d := time.Since(time.Unix(boot, 0))
	if d < 0 {
		return 0, false
	}
	return d, true
}

// readTemperature declines: the SMC sensors are not reachable without
// cgo or a helper binary.
func readTemperature() (float64, bool) { return 0, false }

func readDisk(path string) (total, free uint64, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bs := uint64(st.Bsize)
	// Bavail, not Bfree: the reserve is not space a non-root process can use.
	return st.Blocks * bs, st.Bavail * bs, true
}

// readProcessRSS uses the runtime's accounting; proc_pidinfo is a cgo
// call. It undercounts, which is visibly imperfect rather than absent.
func readProcessRSS() *uint64 { return runtimeRSS() }
