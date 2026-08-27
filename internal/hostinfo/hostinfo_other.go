//go:build !linux

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
// Darwin and Windows implementations land in the next PR; until then this
// keeps every build target compiling, which is the point of having it.

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
