//go:build !linux && !darwin && !windows

package hostinfo

import (
	"os"
	"time"
)

// Platforms without a native implementation yet. Everything static that
// the Go runtime already knows is still reported; every dynamic reading
// declines rather than inventing a value, so the UI shows "—" instead of
// a confident and wrong 0%.
//
// Linux, darwin and windows have real implementations; this covers
// anything else Go can target, so an unusual GOOS still builds and runs
// with an honest set of blanks.

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func detectContainer() bool { return false }

func platformInfo() (kernel, platform string) { return "", "" }

func memoryTotal() (uint64, string) { return 0, "" }

func memoryUsage() (used, avail uint64, ok bool) { return 0, 0, false }

func readLoad() (l1, l5, l15 float64, ok bool) { return 0, 0, 0, false }

func readCPUTimes() (cpuTimes, bool) { return cpuTimes{}, false }

func readUptime() (time.Duration, bool) { return 0, false }

func readTemperature() (float64, bool) { return 0, false }

func readDisk(string) (total, free uint64, ok bool) { return 0, 0, false }

// readProcessRSS falls back to the Go runtime's own accounting: it
// undercounts, but a process reporting no memory use at all would be
// obviously wrong in a way an undercount is not.
func readProcessRSS() *uint64 { return runtimeRSS() }
