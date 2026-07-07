package oui

import "testing"

func TestVendor(t *testing.T) {
	tests := []struct {
		name, mac, want string
	}{
		// 28cdc1 is Raspberry Pi in the generated table; accept every
		// common MAC form for the same OUI.
		{"colon form", "28:cd:c1:aa:bb:cc", "Raspberry Pi"},
		{"dash form", "28-CD-C1-11-22-33", "Raspberry Pi"},
		{"dot form", "28cd.c1aa.bbcc", "Raspberry Pi"},
		{"bare form", "28cdc1aabbcc", "Raspberry Pi"},
		{"uppercase", "28CDC1AABBCC", "Raspberry Pi"},
		{"apple", "00:03:93:00:00:01", "Apple"},
		{"espressif", "00:4b:12:00:00:01", "Espressif"},
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

func TestParseTableToleratesCRLF(t *testing.T) {
	// A git checkout on Windows can rewrite the embedded .tsv to CRLF.
	m := parseTable("aabbcc\tApple\r\n112233\tGoogle\r\n")
	if m["aabbcc"] != "Apple" || m["112233"] != "Google" {
		t.Errorf("CRLF not tolerated: %#v", m)
	}
}

func TestPrefix(t *testing.T) {
	if p := prefix("A4:83:E7:12:34:56"); p != "a483e7" {
		t.Errorf("prefix = %q, want a483e7", p)
	}
	if p := prefix("zz:zz"); p != "" {
		t.Errorf("prefix of junk = %q, want empty", p)
	}
}
