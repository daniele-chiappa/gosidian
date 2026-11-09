// Package fontstest makes the smallest font files the catalog reads.
package fontstest

import (
	"encoding/binary"
	"unicode/utf16"
)

// TrueType is the first four bytes of a TrueType font.
const TrueType = "\x00\x01\x00\x00"

// Build is a font file with only what fonts.Inspect reads: a name table and,
// when fsType >= 0, an OS/2 table. tag is the file's first four bytes.
func Build(tag string, family, style string, fsType int) []byte {
	name := NameTable(map[uint16]string{1: family, 2: style})
	tables := []struct {
		tag  string
		data []byte
	}{{"name", name}}
	if fsType >= 0 {
		os2 := make([]byte, 78)
		binary.BigEndian.PutUint16(os2[8:], uint16(fsType))
		tables = append([]struct {
			tag  string
			data []byte
		}{{"OS/2", os2}}, tables...)
	}
	out := make([]byte, 12+16*len(tables))
	copy(out, tag)
	binary.BigEndian.PutUint16(out[4:], uint16(len(tables)))
	for i, t := range tables {
		rec := out[12+16*i:]
		copy(rec, t.tag)
		binary.BigEndian.PutUint32(rec[8:], uint32(len(out)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(t.data)))
		out = append(out, t.data...)
	}
	return out
}

// NameTable is a name table with those records, in Windows US English.
func NameTable(names map[uint16]string) []byte {
	var strs []byte
	recs := make([]byte, 0, 12*len(names))
	for _, id := range []uint16{1, 2, 16, 17} {
		s, ok := names[id]
		if !ok {
			continue
		}
		enc := make([]byte, 0, 2*len(s))
		for _, u := range utf16.Encode([]rune(s)) {
			enc = binary.BigEndian.AppendUint16(enc, u)
		}
		rec := make([]byte, 12)
		binary.BigEndian.PutUint16(rec[0:], 3)
		binary.BigEndian.PutUint16(rec[2:], 1)
		binary.BigEndian.PutUint16(rec[4:], 0x409)
		binary.BigEndian.PutUint16(rec[6:], id)
		binary.BigEndian.PutUint16(rec[8:], uint16(len(enc)))
		binary.BigEndian.PutUint16(rec[10:], uint16(len(strs)))
		recs = append(recs, rec...)
		strs = append(strs, enc...)
	}
	head := make([]byte, 6)
	binary.BigEndian.PutUint16(head[2:], uint16(len(recs)/12))
	binary.BigEndian.PutUint16(head[4:], uint16(6+len(recs)))
	return append(append(head, recs...), strs...)
}
