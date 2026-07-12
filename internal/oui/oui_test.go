package oui

import (
	"testing"
	"time"
)

func TestVendor(t *testing.T) {
	tests := []struct {
		name, mac, want string
	}{
		// 28cdc1 is Raspberry Pi (MA-L, clean label); accept every common
		// MAC form for the same OUI.
		{"colon form", "28:cd:c1:aa:bb:cc", "Raspberry Pi"},
		{"dash form", "28-CD-C1-11-22-33", "Raspberry Pi"},
		{"dot form", "28cd.c1aa.bbcc", "Raspberry Pi"},
		{"bare form", "28cdc1aabbcc", "Raspberry Pi"},
		{"uppercase", "28CDC1AABBCC", "Raspberry Pi"},
		{"bare 24-bit prefix", "28cdc1", "Raspberry Pi"},
		{"apple", "00:03:93:00:00:01", "Apple"},
		{"espressif", "00:4b:12:00:00:01", "Espressif"},
		// Long-tail MA-L keeps its (suffix-trimmed) organisation name.
		{"long tail MA-L", "28:6f:b9:00:00:01", "Nokia Shanghai Bell"},
		// MA-M (28-bit): C85CE27 belongs to Synergy; the sibling C85CE2\x8
		// block does not, so it must NOT inherit the name.
		{"MA-M hit", "c8:5c:e2:71:22:33", "SYNERGY SYSTEMS AND SOLUTIONS"},
		// MA-S (36-bit) shadows its parent MA-L block (longest prefix wins)…
		{"MA-S shadows MA-L", "8c:1f:64:af:a1:23", "DATA ELECTRONIC DEVICES"},
		// …and in an unassigned gap between carve-outs (8C1F64002 held no
		// MA-S/IAB assignment at generation time) the parent MA-L answers.
		{"MA-L parent fallback", "8c:1f:64:00:2a:bc", "IEEE Registration Authority"},
		// IAB entries live in the same 36-bit space.
		{"IAB hit", "40:d8:55:0d:71:00", "Avant Technologies"},
		{"unknown prefix", "02:00:00:00:00:01", ""},
		{"too short", "28:cd", ""},
		{"empty", "", ""},
		{"junk", "not-a-mac", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Vendor(tt.mac); got != tt.want {
				t.Errorf("Vendor(%q) = %q, want %q", tt.mac, got, tt.want)
			}
		})
	}
}

func TestIsLocallyAdministered(t *testing.T) {
	tests := []struct {
		name, mac string
		want      bool
	}{
		// Second-least-significant bit of the first octet set, unicast:
		// the shape of iOS/Android/Windows randomized private addresses.
		{"x2 form", "02:00:00:aa:bb:cc", true},
		{"x6 form", "d6:11:22:33:44:55", true},
		{"xa form", "6a:11:22:33:44:55", true},
		{"xe form", "be:11:22:33:44:55", true},
		{"factory unicast", "28:cd:c1:aa:bb:cc", false},
		// Multicast (group) addresses are not "private addresses" even
		// with the local bit set.
		{"multicast", "33:33:00:00:00:01", false},
		{"broadcast", "ff:ff:ff:ff:ff:ff", false},
		{"too short", "02:00", false},
		{"junk", "not-a-mac", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLocallyAdministered(tt.mac); got != tt.want {
				t.Errorf("IsLocallyAdministered(%q) = %v, want %v", tt.mac, got, tt.want)
			}
		})
	}
}

func TestParseSlabRejectsCorruptBlob(t *testing.T) {
	for name, blob := range map[string][]byte{
		"empty":       {},
		"bad magic":   []byte("NOPE\x01aaaaaaaaaaaaaaaa"),
		"bad version": []byte("MOUI\x02aaaaaaaaaaaaaaaa"),
		"truncated":   append([]byte("MOUI\x01"), 0xff, 0xff, 0xff, 0x7f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0),
	} {
		t.Run(name, func(t *testing.T) {
			tb := parseSlab(blob)
			if len(tb.k24) != 0 || len(tb.k28) != 0 || len(tb.k36) != 0 {
				t.Errorf("corrupt blob produced a non-empty table")
			}
		})
	}
}

func TestNibbles(t *testing.T) {
	if v, n := nibbles("A4:83:E7:12:34:56"); n != 12 || v != 0xa483e7123456 {
		t.Errorf("nibbles = %012x/%d, want a483e7123456/12", v, n)
	}
	if _, n := nibbles("zz:zz"); n != 0 {
		t.Errorf("nibbles of junk counted %d digits, want 0", n)
	}
}

// The slab decodes once at first use; keep that cost invisible at startup.
func TestLoadCost(t *testing.T) {
	start := time.Now()
	load()
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("table load took %v, want < 50ms", d)
	}
	if len(tbl.k24) < 30000 || len(tbl.k28) < 4000 || len(tbl.k36) < 9000 {
		t.Errorf("table suspiciously small: %d/%d/%d entries",
			len(tbl.k24), len(tbl.k28), len(tbl.k36))
	}
}
