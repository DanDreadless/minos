// Package oui maps a network MAC address to its manufacturer, using the
// full IEEE registry (MA-L/MA-M/MA-S/IAB, ~58k assignments) compiled into a
// compact binary slab by gen.go — sorted prefix arrays plus a deduplicated
// name blob, ~1.3 MB embedded, decoded once on first use. Lookups are
// longest-prefix (36-bit, then 28, then 24) so MA-S/MA-M carve-outs shadow
// their parent MA-L block. Leaf package: stdlib only, used for device
// enrichment off the query hot path.
//
// oui.bin layout (little-endian; see gen.go):
//
//	"MOUI" 0x01
//	n24, n28, n36, namesLen uint32
//	k24 [n24]uint32, o24 [n24]uint32
//	k28 [n28]uint32, o28 [n28]uint32
//	k36 [n36]uint64, o36 [n36]uint32
//	names [namesLen]byte   // \x00-terminated UTF-8 strings
package oui

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"log/slog"
	"slices"
	"sync"
)

//go:embed oui.bin
var raw []byte

type table struct {
	k24, o24 []uint32
	k28, o28 []uint32
	k36      []uint64
	o36      []uint32
	names    []byte
}

var (
	once sync.Once
	tbl  table
)

func load() { tbl = parseSlab(raw) }

// parseSlab decodes the embedded blob. A malformed blob (impossible for a
// committed artifact, but this is defensive code in security software)
// yields an empty table rather than a panic.
func parseSlab(b []byte) table {
	const header = 5 + 4*4
	if len(b) < header || string(b[:5]) != "MOUI\x01" {
		slog.Error("oui: embedded table corrupt; vendor lookups disabled")
		return table{}
	}
	le := binary.LittleEndian
	n24 := int(le.Uint32(b[5:]))
	n28 := int(le.Uint32(b[9:]))
	n36 := int(le.Uint32(b[13:]))
	namesLen := int(le.Uint32(b[17:]))
	want := header + n24*8 + n28*8 + n36*12 + namesLen
	if n24 < 0 || n28 < 0 || n36 < 0 || namesLen < 0 || len(b) != want {
		slog.Error("oui: embedded table corrupt; vendor lookups disabled")
		return table{}
	}
	off := header
	u32s := func(n int) []uint32 {
		out := make([]uint32, n)
		for i := range out {
			out[i] = le.Uint32(b[off:])
			off += 4
		}
		return out
	}
	u64s := func(n int) []uint64 {
		out := make([]uint64, n)
		for i := range out {
			out[i] = le.Uint64(b[off:])
			off += 8
		}
		return out
	}
	var t table
	t.k24 = u32s(n24)
	t.o24 = u32s(n24)
	t.k28 = u32s(n28)
	t.o28 = u32s(n28)
	t.k36 = u64s(n36)
	t.o36 = u32s(n36)
	t.names = b[off : off+namesLen]
	return t
}

// name reads the \x00-terminated string at offset.
func (t table) name(off uint32) string {
	if int(off) >= len(t.names) {
		return ""
	}
	end := bytes.IndexByte(t.names[off:], 0)
	if end < 0 {
		return ""
	}
	return string(t.names[off : int(off)+end])
}

// Vendor returns the manufacturer for a MAC address, or "" if no registry
// prefix covers it. Accepts any common MAC form (colon, dash, dot, or no
// separators). Longest assignment wins: a 36-bit MA-S/IAB block shadows the
// 28-bit MA-M block shadows the 24-bit MA-L block.
func Vendor(mac string) string {
	nib, n := nibbles(mac)
	if n < 6 {
		return "" // not even a 24-bit prefix
	}
	once.Do(load)
	if n >= 9 {
		if i, ok := slices.BinarySearch(tbl.k36, nib>>(48-36)); ok {
			return tbl.name(tbl.o36[i])
		}
	}
	if n >= 7 {
		if i, ok := slices.BinarySearch(tbl.k28, uint32(nib>>(48-28))); ok {
			return tbl.name(tbl.o28[i])
		}
	}
	if i, ok := slices.BinarySearch(tbl.k24, uint32(nib>>(48-24))); ok {
		return tbl.name(tbl.o24[i])
	}
	return ""
}

// IsLocallyAdministered reports whether the MAC has the locally-administered
// bit set (and is not a group/multicast address) — the shape of the
// randomized "private addresses" iOS/Android/Windows generate, which no
// registry can ever name. Returns false for anything unparsable.
func IsLocallyAdministered(mac string) bool {
	nib, n := nibbles(mac)
	if n < 12 {
		return false
	}
	first := byte(nib >> 40) // first octet
	return first&0x02 != 0 && first&0x01 == 0
}

// nibbles packs up to 12 hex digits of a MAC (separators skipped) into the
// top of a uint64: digit i lands at bits [44-4i, 48-4i). Returns how many
// digits were found; fewer than 12 means the input wasn't a full MAC.
func nibbles(mac string) (packed uint64, count int) {
	for i := 0; i < len(mac) && count < 12; i++ {
		var v uint64
		switch c := mac[i]; {
		case c >= '0' && c <= '9':
			v = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = uint64(c-'A') + 10
		default:
			continue
		}
		packed |= v << (44 - 4*count)
		count++
	}
	return packed, count
}
