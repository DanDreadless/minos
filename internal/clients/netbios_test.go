package clients

import (
	"encoding/binary"
	"testing"
)

func TestEncodeNetBIOSName(t *testing.T) {
	got := string(encodeNetBIOSName("*"))
	// '*' (0x2A) → "CK"; each trailing NUL (0x00) → "AA".
	want := "CK" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if len(want) != 32 {
		t.Fatalf("bad test constant: want length %d, not 32", len(want))
	}
	if got != want {
		t.Errorf("encodeNetBIOSName(%q) = %q, want %q", "*", got, want)
	}
}

func TestBuildNBStatQuery(t *testing.T) {
	q := buildNBStatQuery()
	if len(q) != 50 {
		t.Fatalf("query length = %d, want 50", len(q))
	}
	if got := binary.BigEndian.Uint16(q[4:6]); got != 1 {
		t.Errorf("QDCOUNT = %d, want 1", got)
	}
	if q[12] != 0x20 {
		t.Errorf("label length prefix = %#x, want 0x20", q[12])
	}
	if string(q[13:45]) != string(encodeNetBIOSName("*")) {
		t.Errorf("encoded name mismatch")
	}
	if q[45] != 0x00 {
		t.Errorf("name terminator = %#x, want 0x00", q[45])
	}
	if got := binary.BigEndian.Uint16(q[46:48]); got != nbstatType {
		t.Errorf("QTYPE = %#x, want %#x", got, nbstatType)
	}
	if got := binary.BigEndian.Uint16(q[48:50]); got != nbClassIN {
		t.Errorf("QCLASS = %#x, want %#x", got, nbClassIN)
	}
}

// nbEntry is one name-table entry for the response builder.
type nbEntry struct {
	name   string // written as-is into the 15-byte field, then space-padded
	suffix byte
	group  bool
}

// buildNBStatResp assembles a node-status reply with the given name entries,
// echoing the wildcard question name in full (the common, uncompressed form).
func buildNBStatResp(entries []nbEntry) []byte {
	var b []byte
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[0:2], 0x1234) // txn id
	hdr[2] = 0x84                                // response + authoritative
	binary.BigEndian.PutUint16(hdr[6:8], 1)      // ANCOUNT = 1
	b = append(b, hdr...)

	name := encodeNetBIOSName("*")
	b = append(b, byte(len(name)))
	b = append(b, name...)
	b = append(b, 0x00)

	b = binary.BigEndian.AppendUint16(b, nbstatType)
	b = binary.BigEndian.AppendUint16(b, nbClassIN)
	b = append(b, 0, 0, 0, 0) // TTL

	rdata := []byte{byte(len(entries))}
	for _, e := range entries {
		var field [18]byte
		nm := e.name
		if len(nm) > 15 {
			nm = nm[:15]
		}
		copy(field[:15], nm)
		for i := len(nm); i < 15; i++ {
			field[i] = ' '
		}
		field[15] = e.suffix
		if e.group {
			binary.BigEndian.PutUint16(field[16:18], 0x8000)
		}
		rdata = append(rdata, field[:]...)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(len(rdata)))
	return append(b, rdata...)
}

func TestParseNBStatResponse(t *testing.T) {
	// The real-world case: a workgroup group name, the unique computer name,
	// and the same name under the Server service — we want the computer name.
	typical := buildNBStatResp([]nbEntry{
		{name: "WORKGROUP", suffix: 0x00, group: true},
		{name: "MYPC", suffix: 0x00, group: false},
		{name: "MYPC", suffix: 0x20, group: false},
	})

	// A response whose answer name is a compression pointer, not the full name.
	pointer := []byte{
		0x12, 0x34, 0x84, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xC0, 0x0C, // name: pointer to offset 12
	}
	pointer = binary.BigEndian.AppendUint16(pointer, nbstatType)
	pointer = binary.BigEndian.AppendUint16(pointer, nbClassIN)
	pointer = append(pointer, 0, 0, 0, 0) // TTL
	prd := []byte{1}
	var pf [18]byte
	copy(pf[:15], "POINTERPC")
	for i := len("POINTERPC"); i < 15; i++ {
		pf[i] = ' '
	}
	prd = append(prd, pf[:]...)
	pointer = binary.BigEndian.AppendUint16(pointer, uint16(len(prd)))
	pointer = append(pointer, prd...)

	// numNames claims 5 entries but only one is present.
	overclaim := buildNBStatResp([]nbEntry{{name: "MYPC", suffix: 0x00}})
	overclaim[12+34+10] = 5 // header(12) + full name(34) + type/class/ttl/rdlen(10)

	tests := []struct {
		name string
		resp []byte
		want string
	}{
		{"typical", typical, "MYPC"},
		{"compression pointer", pointer, "POINTERPC"},
		{"only group 0x00", buildNBStatResp([]nbEntry{{name: "WORKGROUP", suffix: 0x00, group: true}}), ""},
		{"only server suffix", buildNBStatResp([]nbEntry{{name: "MYPC", suffix: 0x20}}), ""},
		{"all-spaces name", buildNBStatResp([]nbEntry{{name: "               ", suffix: 0x00}}), ""},
		{"non-printable name", buildNBStatResp([]nbEntry{{name: "MY\x01PC", suffix: 0x00}}), ""},
		{"zero names", buildNBStatResp(nil), ""},
		{"header only", make([]byte, 12), ""},
		{"empty", nil, ""},
		{"truncated mid-entry", typical[:len(typical)-5], ""},
		{"numNames overclaim", overclaim, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNBStatResponse(tc.resp); got != tc.want {
				t.Errorf("parseNBStatResponse() = %q, want %q", got, tc.want)
			}
		})
	}
}
