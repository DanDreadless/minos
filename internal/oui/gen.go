//go:build ignore

// Generator for oui.bin — the full IEEE MAC-prefix → vendor table as a
// compact binary slab (sorted prefix arrays + a deduplicated name blob).
//
// Consumes all four IEEE registries (MA-L 24-bit, MA-M 28-bit, MA-S and
// IAB 36-bit; ~58k assignments) instead of the old curated subset — the
// slab encoding makes the full registry cost ~1.3 MB, noise against the
// memory budget. Big consumer brands still get clean display labels via
// the rules below; the long tail keeps its (suffix-trimmed) IEEE
// organisation name. Run:
//
//	curl -fsSL -o oui.csv    https://standards-oui.ieee.org/oui/oui.csv
//	curl -fsSL -o mam.csv    https://standards-oui.ieee.org/oui28/mam.csv
//	curl -fsSL -o oui36.csv  https://standards-oui.ieee.org/oui36/oui36.csv
//	curl -fsSL -o iab.csv    https://standards-oui.ieee.org/iab/iab.csv
//	go run gen.go oui.bin oui.csv mam.csv oui36.csv iab.csv
//
// then commit oui.bin. The file layout is documented in oui.go beside the
// loader; bump the magic's version byte if it ever changes.
package main

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
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

// corpSuffixes are trimmed (repeatedly, case-insensitively) from the end of
// long-tail organisation names so "Acme Networks Co., Ltd." displays as
// "Acme Networks". Punctuation/whitespace between suffixes is re-trimmed
// each round.
var corpSuffixes = []string{
	"co., ltd.", "co.,ltd.", "co., ltd", "co.,ltd", "co ltd", "co., limited",
	"company limited", "corporation", "incorporated", "limited", "l.l.c.",
	"gmbh & co. kg", "inc.", "inc", "llc", "ltd.", "ltd", "gmbh", "corp.",
	"corp", "s.a.", "a.s.", "b.v.", "s.r.l.", "pty", "co.", "ag", "sa", "bv",
}

func label(org string) string {
	l := strings.ToLower(org)
	for _, r := range rules {
		if strings.Contains(l, r.pattern) {
			return r.label
		}
	}
	return cleanOrg(org)
}

// cleanOrg tidies a raw organisation name for display: control characters
// dropped, corporate suffixes trimmed, whitespace collapsed, length capped.
func cleanOrg(org string) string {
	var b strings.Builder
	for _, r := range org {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	for {
		trimmed := strings.TrimRight(s, " ,.-")
		low := strings.ToLower(trimmed)
		cut := false
		for _, suf := range corpSuffixes {
			if strings.HasSuffix(low, suf) && len(trimmed) > len(suf) {
				trimmed = strings.TrimRight(trimmed[:len(trimmed)-len(suf)], " ,.-")
				cut = true
				break
			}
		}
		s = trimmed
		if !cut {
			break
		}
	}
	if len(s) > 64 {
		s = strings.TrimSpace(s[:64])
	}
	return s
}

type entry struct {
	key  uint64
	name string
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: go run gen.go <oui.bin> <registry.csv>...")
		os.Exit(2)
	}

	byBits := map[int][]entry{24: nil, 28: nil, 36: nil}
	seen := map[int]map[uint64]bool{24: {}, 28: {}, 36: {}}
	dupes, skipped := 0, 0

	for _, path := range os.Args[2:] {
		in, err := os.Open(path)
		must(err)
		r := csv.NewReader(in)
		r.FieldsPerRecord = -1
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			must(err)
			if len(rec) < 3 || rec[0] == "Registry" {
				continue
			}
			assignment := strings.ToLower(strings.TrimSpace(rec[1]))
			var bits int
			switch len(assignment) {
			case 6:
				bits = 24
			case 7:
				bits = 28
			case 9:
				bits = 36
			default:
				skipped++
				continue
			}
			key, err := strconv.ParseUint(assignment, 16, 64)
			if err != nil {
				skipped++
				continue
			}
			name := label(rec[2])
			if name == "" {
				skipped++
				continue
			}
			if seen[bits][key] {
				dupes++
				continue // first assignment wins
			}
			seen[bits][key] = true
			byBits[bits] = append(byBits[bits], entry{key, name})
		}
		in.Close()
	}

	// Deduplicate names into one \x00-terminated blob.
	var names []byte
	offsets := map[string]uint32{}
	offsetOf := func(name string) uint32 {
		if off, ok := offsets[name]; ok {
			return off
		}
		off := uint32(len(names))
		offsets[name] = off
		names = append(names, name...)
		names = append(names, 0)
		return off
	}

	out, err := os.Create(os.Args[1])
	must(err)
	le := binary.LittleEndian
	write := func(v any) { must(binary.Write(out, le, v)) }

	// Header: magic + version, three counts, name-blob length.
	_, err = out.Write([]byte("MOUI\x01"))
	must(err)
	for _, bits := range []int{24, 28, 36} {
		sort.Slice(byBits[bits], func(i, j int) bool { return byBits[bits][i].key < byBits[bits][j].key })
	}
	// Assign name offsets in a deterministic (sorted-key) order so the
	// output is reproducible for a given input set.
	offs := map[int][]uint32{}
	for _, bits := range []int{24, 28, 36} {
		for _, e := range byBits[bits] {
			offs[bits] = append(offs[bits], offsetOf(e.name))
		}
	}
	write(uint32(len(byBits[24])))
	write(uint32(len(byBits[28])))
	write(uint32(len(byBits[36])))
	write(uint32(len(names)))
	for _, e := range byBits[24] {
		write(uint32(e.key))
	}
	write(offs[24])
	for _, e := range byBits[28] {
		write(uint32(e.key))
	}
	write(offs[28])
	for _, e := range byBits[36] {
		write(e.key)
	}
	write(offs[36])
	_, err = out.Write(names)
	must(err)
	must(out.Close())

	st, err := os.Stat(os.Args[1])
	must(err)
	fmt.Fprintf(os.Stderr,
		"wrote %s: %d bytes — %d MA-L + %d MA-M + %d MA-S/IAB prefixes, %d unique names (%d dupes, %d skipped)\n",
		os.Args[1], st.Size(), len(byBits[24]), len(byBits[28]), len(byBits[36]),
		len(offsets), dupes, skipped)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
