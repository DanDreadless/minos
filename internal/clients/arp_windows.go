package clients

import (
	"os/exec"
	"regexp"
	"strings"
)

// arpLine matches "  192.168.1.10     aa-bb-cc-dd-ee-ff     dynamic".
var arpLine = regexp.MustCompile(`^\s*(\d{1,3}(?:\.\d{1,3}){3})\s+([0-9a-fA-F]{2}(?:-[0-9a-fA-F]{2}){5})\s`)

// readARPTable shells out to `arp -a` — the portable option on Windows
// without cgo or undocumented syscalls. Dev-convenience only; the deploy
// targets are Linux.
func readARPTable() map[string]string {
	outBytes, err := exec.Command("arp", "-a").Output()
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(outBytes), "\n") {
		m := arpLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		mac := strings.ToLower(strings.ReplaceAll(m[2], "-", ":"))
		if mac == "ff:ff:ff:ff:ff:ff" || mac == "00:00:00:00:00:00" {
			continue // broadcast/incomplete
		}
		out[m[1]] = mac
	}
	return out
}
