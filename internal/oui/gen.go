//go:build ignore

// Generator for oui.tsv — the curated MAC-prefix → vendor table.
//
// The full IEEE registry is ~40k rows; we keep only common consumer/IoT
// vendors (per the memory budget) and give each a clean display label. Run:
//
//	curl -fsSL -o oui.csv https://standards-oui.ieee.org/oui/oui.csv
//	go run gen.go oui.csv oui.tsv
//
// then commit oui.tsv. Add a vendor by extending rules below and rerunning.
package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// rules map a lowercase substring of the IEEE "Organization Name" to a clean
// vendor label. Order matters — the first match wins, so put specific
// patterns before broad ones. Patterns are chosen to be specific enough to
// avoid false positives on unrelated organisations.
var rules = []struct{ pattern, label string }{
	{"apple, inc", "Apple"},
	{"samsung", "Samsung"},
	{"google", "Google"},
	{"nest labs", "Google Nest"},
	{"amazon", "Amazon"},
	{"espressif", "Espressif"},
	{"raspberry pi", "Raspberry Pi"},
	{"intel corp", "Intel"},
	{"microsoft", "Microsoft"},
	{"sonos", "Sonos"},
	{"roku", "Roku"},
	{"tp-link", "TP-Link"},
	{"netgear", "Netgear"},
	{"ubiquiti", "Ubiquiti"},
	{"cisco", "Cisco"},
	{"dell inc", "Dell"},
	{"hewlett packard", "HP"},
	{"hewlett-packard", "HP"},
	{"hp inc", "HP"},
	{"lg electronics", "LG"},
	{"xiaomi", "Xiaomi"},
	{"huawei", "Huawei"},
	{"realtek", "Realtek"},
	{"nintendo", "Nintendo"},
	{"sony corporation", "Sony"},
	{"sony interactive", "Sony"},
	{"signify", "Philips Hue"},
	{"philips lighting", "Philips Hue"},
	{"ring llc", "Ring"},
	{"wyze", "Wyze"},
	{"tuya", "Tuya"},
	{"belkin", "Belkin"},
	{"eero", "eero"},
	{"arlo", "Arlo"},
	{"bose", "Bose"},
	{"sound united", "Denon"},
	{"yamaha", "Yamaha"},
	{"honeywell", "Honeywell"},
	{"ecobee", "ecobee"},
	{"fitbit", "Fitbit"},
	{"gopro", "GoPro"},
	{"synology", "Synology"},
	{"qnap", "QNAP"},
	{"asustek", "ASUS"},
	{"d-link", "D-Link"},
	{"zyxel", "Zyxel"},
	{"aruba", "Aruba"},
	{"avm gmbh", "AVM"},
	{"texas instruments", "Texas Instruments"},
	{"murata", "Murata"},
	{"azurewave", "AzureWave"},
	{"liteon", "Liteon"},
	{"lite-on", "Liteon"},
	{"netatmo", "Netatmo"},
	{"allterco", "Shelly"},
	{"itead", "Sonoff"},
	{"reolink", "Reolink"},
	{"hikvision", "Hikvision"},
	{"dahua", "Dahua"},
	{"irobot", "iRobot"},
	{"garmin", "Garmin"},
	{"canon", "Canon"},
	{"seiko epson", "Epson"},
	{"brother", "Brother"},
	{"lenovo", "Lenovo"},
	{"acer", "Acer"},
	{"panasonic", "Panasonic"},
	{"vizio", "Vizio"},
	{"hisense", "Hisense"},
	{"roborock", "Roborock"},
}

func label(org string) string {
	l := strings.ToLower(org)
	for _, r := range rules {
		if strings.Contains(l, r.pattern) {
			return r.label
		}
	}
	return ""
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run gen.go <oui.csv> <oui.tsv>")
		os.Exit(2)
	}
	in, err := os.Open(os.Args[1])
	must(err)
	defer in.Close()

	r := csv.NewReader(in)
	r.FieldsPerRecord = -1
	type row struct{ prefix, vendor string }
	var rows []row
	counts := map[string]int{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		must(err)
		if len(rec) < 3 || rec[0] == "Registry" {
			continue
		}
		v := label(rec[2])
		if v == "" {
			continue
		}
		prefix := strings.ToLower(strings.TrimSpace(rec[1]))
		if len(prefix) != 6 {
			continue
		}
		rows = append(rows, row{prefix, v})
		counts[v]++
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].prefix < rows[j].prefix })

	out, err := os.Create(os.Args[2])
	must(err)
	w := bufio.NewWriter(out)
	for _, rw := range rows {
		fmt.Fprintf(w, "%s\t%s\n", rw.prefix, rw.vendor)
	}
	must(w.Flush())
	must(out.Close())

	// Summary to stderr for a sanity check.
	type vc struct {
		v string
		c int
	}
	var vcs []vc
	for v, c := range counts {
		vcs = append(vcs, vc{v, c})
	}
	sort.Slice(vcs, func(i, j int) bool { return vcs[i].c > vcs[j].c })
	fmt.Fprintf(os.Stderr, "wrote %d prefixes across %d vendors\n", len(rows), len(counts))
	for _, x := range vcs {
		fmt.Fprintf(os.Stderr, "  %-20s %d\n", x.v, x.c)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
