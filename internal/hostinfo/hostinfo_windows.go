//go:build windows

package hostinfo

import (
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows exports RtlGetVersion and GetDiskFreeSpaceEx but not the
// three counters below, so they are declared here rather than pulling in
// another dependency for three calls.
var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
	procGetTickCount64     = kernel32.NewProc("GetTickCount64")
)

// memoryStatusEx mirrors MEMORYSTATUSEX. Length must be set to the
// struct size before the call or the kernel rejects it.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// Windows is a development target for Minos, not a deployment one, but
// it builds and ships, so the panel should not be blank on it.
//
// Load average has no Windows equivalent — it is a Unix run-queue
// concept, not a universal one — so it declines rather than inventing a
// number. CPU utilisation is available instead, which is the metric a
// Windows user would look for anyway.

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// detectContainer: Windows containers are rare and Minos is not shipped
// for them; the Docker path on Windows runs Linux images, which report
// GOOS=linux and take the Linux implementation.
func detectContainer() bool { return false }

func platformInfo() (kernel, platform string) {
	v := windows.RtlGetVersion()
	if v == nil {
		return "", ""
	}
	kernel = itoa(uint64(v.MajorVersion)) + "." + itoa(uint64(v.MinorVersion)) +
		"." + itoa(uint64(v.BuildNumber))
	return kernel, "Windows " + kernel
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func memoryStatus() (total, avail uint64, ok bool) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0, false
	}
	return m.TotalPhys, m.AvailPhys, true
}

func memoryTotal() (uint64, string) {
	total, _, ok := memoryStatus()
	if !ok {
		return 0, ""
	}
	return total, MemoryHost
}

func memoryUsage() (used, avail uint64, ok bool) {
	total, available, ok := memoryStatus()
	if !ok || available > total {
		return 0, 0, false
	}
	return total - available, available, true
}

// readLoad declines: Windows has no run-queue load average, and a
// fabricated one would be worse than an honest blank.
func readLoad() (l1, l5, l15 float64, ok bool) { return 0, 0, 0, false }

// readCPUTimes uses GetSystemTimes. Its "kernel" bucket includes idle,
// so busy is (kernel + user) - idle and total is (kernel + user).
func readCPUTimes() (cpuTimes, bool) {
	var idle, kernel, user windows.Filetime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return cpuTimes{}, false
	}
	i, k, u := filetimeTicks(idle), filetimeTicks(kernel), filetimeTicks(user)
	total := k + u
	if total < i {
		return cpuTimes{}, false
	}
	return cpuTimes{busy: total - i, total: total}, true
}

func filetimeTicks(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

func readUptime() (time.Duration, bool) {
	r, _, _ := procGetTickCount64.Call()
	if r == 0 {
		return 0, false
	}
	return time.Duration(uint64(r)) * time.Millisecond, true
}

// readTemperature declines: reaching CPU temperature on Windows means
// WMI or a vendor driver, neither of which belongs in this binary.
func readTemperature() (float64, bool) { return 0, false }

func readDisk(path string) (total, free uint64, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	// freeAvail is what this user may use; totalFree includes quota space
	// they may not, so the caller gets the honest figure.
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &totalBytes, &totalFree); err != nil {
		return 0, 0, false
	}
	return totalBytes, freeAvail, true
}

// readProcessRSS uses the runtime's accounting. The working set would
// need a fourth lazy-loaded psapi call for a number that only matters on
// a development machine; the undercount is an acceptable trade.
func readProcessRSS() *uint64 { return runtimeRSS() }
