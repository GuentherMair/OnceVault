// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

// Command gen_maskfont generates the "OnceVault Disc" mask font: a minimal
// TrueType font in which EVERY Unicode codepoint (cmap format 13, backed by a
// format 4 subtable for ASCII) maps to one centered disc glyph at a fixed
// 600/1000-em advance. Applied to both the masked textarea and its dot-mirror
// overlay, it makes caret, selection, and soft-wrap positions align with the
// mask dots BY CONSTRUCTION — every character, including CJK and emoji,
// occupies exactly one disc advance in both layers. Stdlib only,
// deterministic (fixed timestamps).
//
// Usage: go run ./tools/gen_maskfont [output.ttf]   (default web/maskfont.ttf)
// The base64 of the output is embedded as a data: URI in web/index.html.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

const (
	unitsPerEm = 1000
	advance    = 600 // fixed advance for every glyph, .notdef included
	ascender   = 800
	descender  = -200
)

// Disc geometry: a circle approximated by 8 off-curve quadratic points on an
// octagon (implied on-curve midpoints land on the circle). Radius 125 units
// (~4px at 16px), optically centered a touch above the mid x-height like the
// typical U+2022 bullet.
var discPts = [8][2]int16{
	{435, 310}, {395, 405}, {300, 445}, {205, 405},
	{165, 310}, {205, 215}, {300, 175}, {395, 215},
}

const (
	xMin, yMin, xMax, yMax = 165, 175, 435, 445
)

func be(w *bytes.Buffer, vs ...any) {
	for _, v := range vs {
		binary.Write(w, binary.BigEndian, v)
	}
}

// glyfTable returns glyf bytes and the short-format loca offsets for
// [.notdef (empty), disc].
func glyfTable() (glyf []byte, loca []uint16) {
	var g bytes.Buffer
	be(&g, int16(1), int16(xMin), int16(yMin), int16(xMax), int16(yMax)) // 1 contour + bbox
	be(&g, uint16(7), uint16(0))                                         // endPt of contour, no instructions
	for range discPts {
		g.WriteByte(0x00) // off-curve, 16-bit deltas
	}
	var px, py int16
	for _, p := range discPts {
		be(&g, p[0]-px)
		px = p[0]
	}
	for _, p := range discPts {
		be(&g, p[1]-py)
		py = p[1]
	}
	for g.Len()%4 != 0 {
		g.WriteByte(0)
	}
	// loca (short): offset/2 per glyph boundary; .notdef is zero-length.
	return g.Bytes(), []uint16{0, 0, uint16(g.Len() / 2)}
}

// cmapTable maps ASCII via format 4 and all of Unicode via format 13 to the
// disc glyph (id 1).
func cmapTable() []byte {
	var f4 bytes.Buffer
	const segX2 = 4 // 2 segments: 0x20-0x7E and the 0xFFFF terminator
	glyphIDs := 0x7E - 0x20 + 1
	be(&f4, uint16(4), uint16(14+4+2+4+4+4+2*glyphIDs), uint16(0))
	be(&f4, uint16(segX2), uint16(4), uint16(1), uint16(0)) // searchRange/entrySelector/rangeShift
	be(&f4, uint16(0x7E), uint16(0xFFFF))                   // endCode
	be(&f4, uint16(0))                                      // reservedPad
	be(&f4, uint16(0x20), uint16(0xFFFF))                   // startCode
	be(&f4, int16(0), int16(1))                             // idDelta (terminator: 0xFFFF+1 → .notdef)
	be(&f4, uint16(4), uint16(0))                           // idRangeOffset: seg0 → glyphIdArray[0]
	for i := 0; i < glyphIDs; i++ {
		be(&f4, uint16(1))
	}

	var f13 bytes.Buffer
	be(&f13, uint16(13), uint16(0), uint32(28), uint32(0), uint32(1))
	be(&f13, uint32(0x0000), uint32(0x10FFFF), uint32(1)) // every codepoint → disc

	var c bytes.Buffer
	be(&c, uint16(0), uint16(2)) // version, numTables
	off4 := 4 + 2*8
	be(&c, uint16(3), uint16(1), uint32(off4))           // Windows BMP → format 4
	be(&c, uint16(3), uint16(10), uint32(off4+f4.Len())) // Windows full → format 13
	c.Write(f4.Bytes())
	c.Write(f13.Bytes())
	return c.Bytes()
}

func headTable() []byte {
	var b bytes.Buffer
	be(&b, uint32(0x00010000), uint32(0x00010000)) // version, fontRevision
	be(&b, uint32(0))                              // checkSumAdjustment (patched later)
	be(&b, uint32(0x5F0F3CF5))                     // magicNumber
	be(&b, uint16(0x0003), uint16(unitsPerEm))
	be(&b, int64(0), int64(0)) // created/modified: fixed for determinism
	be(&b, int16(xMin), int16(yMin), int16(xMax), int16(yMax))
	be(&b, uint16(0), uint16(6))         // macStyle, lowestRecPPEM
	be(&b, int16(2), int16(0), int16(0)) // fontDirectionHint, indexToLocFormat (short), glyphDataFormat
	return b.Bytes()
}

func hheaTable() []byte {
	var b bytes.Buffer
	be(&b, uint32(0x00010000), int16(ascender), int16(descender), int16(0))
	be(&b, uint16(advance))                            // advanceWidthMax
	be(&b, int16(0), int16(advance-xMax), int16(xMax)) // minLSB, minRSB, xMaxExtent
	be(&b, int16(1), int16(0), int16(0))               // caret slope rise/run/offset
	be(&b, int16(0), int16(0), int16(0), int16(0))     // reserved
	be(&b, int16(0), uint16(2))                        // metricDataFormat, numberOfHMetrics
	return b.Bytes()
}

func hmtxTable() []byte {
	var b bytes.Buffer
	be(&b, uint16(advance), int16(0))    // .notdef
	be(&b, uint16(advance), int16(xMin)) // disc
	return b.Bytes()
}

func maxpTable() []byte {
	var b bytes.Buffer
	be(&b, uint32(0x00010000), uint16(2)) // v1.0, numGlyphs
	be(&b, uint16(8), uint16(1))          // maxPoints, maxContours
	be(&b, uint16(0), uint16(0), uint16(2), uint16(0), uint16(0), uint16(0),
		uint16(0), uint16(0), uint16(0), uint16(0), uint16(0))
	return b.Bytes()
}

func os2Table() []byte {
	var b bytes.Buffer
	be(&b, uint16(4), int16(advance), uint16(400), uint16(5), uint16(0))                               // version, xAvgCharWidth, weight, width, fsType
	be(&b, int16(650), int16(600), int16(0), int16(75), int16(650), int16(600), int16(350), int16(75)) // sub/superscript
	be(&b, int16(50), int16(250), int16(0))                                                            // strikeout size/pos, familyClass
	b.Write(make([]byte, 10))                                                                          // panose
	be(&b, uint32(1), uint32(0), uint32(0), uint32(0))                                                 // ulUnicodeRange
	b.WriteString("OVLT")                                                                              // achVendID
	be(&b, uint16(0x0040), uint16(0x20), uint16(0xFFFF))                                               // fsSelection REGULAR, first/last char
	be(&b, int16(ascender), int16(descender), int16(0))                                                // sTypo*
	be(&b, uint16(ascender), uint16(-descender))                                                       // usWin*
	be(&b, uint32(1), uint32(0))                                                                       // ulCodePageRange
	be(&b, int16(500), int16(700), uint16(0), uint16(0x20), uint16(1))                                 // sxHeight, sCapHeight, default/break char, maxContext
	return b.Bytes()
}

func nameTable() []byte {
	type rec struct {
		id  uint16
		val string
	}
	recs := []rec{
		{1, "OnceVault Disc"}, {2, "Regular"}, {3, "OnceVaultDisc-2026"},
		{4, "OnceVault Disc"}, {6, "OnceVaultDisc"},
	}
	var strs bytes.Buffer
	var b bytes.Buffer
	be(&b, uint16(0), uint16(len(recs)), uint16(6+12*len(recs)))
	for _, r := range recs {
		off := strs.Len()
		for _, c := range r.val { // ASCII-only → trivial UTF-16BE
			be(&strs, uint16(c))
		}
		be(&b, uint16(3), uint16(1), uint16(0x0409), r.id, uint16(strs.Len()-off), uint16(off))
	}
	b.Write(strs.Bytes())
	return b.Bytes()
}

func postTable() []byte {
	var b bytes.Buffer
	be(&b, uint32(0x00030000), uint32(0), int16(-100), int16(50), uint32(1)) // v3, italic, underline, isFixedPitch
	be(&b, uint32(0), uint32(0), uint32(0), uint32(0))                       // memory hints
	return b.Bytes()
}

func checksum(data []byte) uint32 {
	var sum uint32
	for i := 0; i < len(data); i += 4 {
		var v uint32
		for j := 0; j < 4; j++ {
			v <<= 8
			if i+j < len(data) {
				v |= uint32(data[i+j])
			}
		}
		sum += v
	}
	return sum
}

func main() {
	path := "web/maskfont.ttf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	glyf, loca := glyfTable()
	var locaB bytes.Buffer
	for _, o := range loca {
		be(&locaB, o)
	}

	tables := map[string][]byte{
		"OS/2": os2Table(), "cmap": cmapTable(), "glyf": glyf,
		"head": headTable(), "hhea": hheaTable(), "hmtx": hmtxTable(),
		"loca": locaB.Bytes(), "maxp": maxpTable(), "name": nameTable(),
		"post": postTable(),
	}
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	var font bytes.Buffer
	n := uint16(len(tags))
	sr := uint16(128) // 16 * 2^floor(log2(10))
	be(&font, uint32(0x00010000), n, sr, uint16(3), n*16-sr)
	offset := uint32(12 + 16*len(tags))
	type entry struct {
		tag string
		off uint32
	}
	var entries []entry
	for _, t := range tags {
		data := tables[t]
		be(&font, []byte(t), checksum(data), offset, uint32(len(data)))
		entries = append(entries, entry{t, offset})
		offset += uint32((len(data) + 3) &^ 3)
	}
	var headOff uint32
	for i, t := range tags {
		data := tables[t]
		if t == "head" {
			headOff = entries[i].off
		}
		font.Write(data)
		for font.Len()%4 != 0 {
			font.WriteByte(0)
		}
	}

	// head.checkSumAdjustment over the whole font.
	raw := font.Bytes()
	adj := 0xB1B0AFBA - checksum(raw)
	binary.BigEndian.PutUint32(raw[headOff+8:], adj)

	if err := os.WriteFile(path, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen_maskfont: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, base64 %d chars)\n", path, len(raw),
		len(base64.StdEncoding.EncodeToString(raw)))
}
