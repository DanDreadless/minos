// Package oui maps a network MAC address to its manufacturer, using a curated
// subset of the IEEE OUI registry (common consumer/IoT vendors — the full
// registry is ~40k rows and would blow the memory budget). The table lives in
// oui.tsv, generated from the IEEE CSV by gen.go; see that file to regenerate
// or add a vendor. Leaf package: stdlib only, imported for device enrichment
// off the query hot path.
package oui

import (
	_ "embed"
	"strings"
	"sync"
)

//go:embed oui.tsv
var raw string

var (
	once  sync.Once
	table map[string]string
)

func load() { table = parseTable(raw) }

func parseTable(raw string) map[string]string {
	m := make(map[string]string, 11000)
	for _, line := range strings.Split(raw, "\n") {
		// Tolerate CRLF: git may convert the embedded .tsv on a Windows
		// checkout, which would otherwise leave "\r" on each vendor name.
		line = strings.TrimRight(line, "\r")
		if prefix, vendor, ok := strings.Cut(line, "\t"); ok {
			m[prefix] = vendor
		}
	}
	return m
}

// Vendor returns the manufacturer for a MAC address, or "" if its OUI prefix
// isn't in the curated table. Accepts any common MAC form (colon, dash, dot,
// or no separators); only the leading 24-bit OUI is consulted.
func Vendor(mac string) string {
	p := prefix(mac)
	if p == "" {
		return ""
	}
	once.Do(load)
	return table[p]
}

// prefix normalises a MAC to its lowercase 6-hex OUI ("A4:83:E7:…" → "a483e7"),
// or "" if there aren't at least three hex octets. Non-hex bytes (separators)
// are skipped.
func prefix(mac string) string {
	var b strings.Builder
	for i := 0; i < len(mac) && b.Len() < 6; i++ {
		switch c := mac[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			b.WriteByte(c)
		case c >= 'A' && c <= 'F':
			b.WriteByte(c + ('a' - 'A'))
		}
	}
	if b.Len() < 6 {
		return ""
	}
	return b.String()
}
