// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

// Command gen_favicon regenerates web/favicon.ico: a white vault wheel (ring +
// axis cross spokes + hub) on a rounded blue square, rendered at 16/32/48 px
// and packed as a PNG-in-ICO container. Stdlib only, deterministic.
//
// Geometry (on a 100-unit canvas, per docs/PLAN_STEP_0.md §0.2): rounded
// square corner radius 22, ring radius 26 with halfwidth 5.5, four
// axis-aligned spokes of halfwidth 5.5 reaching radius 40, hub radius 12.
// Every pixel is supersampled 8×8.
//
// Usage: go run ./tools/gen_favicon [output.ico]  (default web/favicon.ico)
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	canvas       = 100.0 // logical units per icon edge
	cornerRadius = 22.0
	ringRadius   = 26.0
	ringHalf     = 5.5
	spokeHalf    = 5.5
	spokeReach   = 40.0
	hubRadius    = 12.0
	supersample  = 8
)

var (
	blue  = color.NRGBA{R: 0x1a, G: 0x73, B: 0xe8, A: 0xff} // frontend focus blue
	white = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

// inRoundedSquare reports whether (x,y) lies inside the rounded square that
// spans the full canvas with cornerRadius corners.
func inRoundedSquare(x, y float64) bool {
	if x < 0 || y < 0 || x > canvas || y > canvas {
		return false
	}
	cx := math.Max(cornerRadius-x, math.Max(0, x-(canvas-cornerRadius)))
	cy := math.Max(cornerRadius-y, math.Max(0, y-(canvas-cornerRadius)))
	if cx > 0 && cy > 0 {
		return cx*cx+cy*cy <= cornerRadius*cornerRadius
	}
	return true
}

// inWheel reports whether (x,y) lies on the white wheel: ring, spokes, or hub.
func inWheel(x, y float64) bool {
	dx, dy := x-canvas/2, y-canvas/2
	d := math.Sqrt(dx*dx + dy*dy)
	if d <= hubRadius {
		return true
	}
	if math.Abs(d-ringRadius) <= ringHalf {
		return true
	}
	if d <= spokeReach && (math.Abs(dx) <= spokeHalf || math.Abs(dy) <= spokeHalf) {
		return true
	}
	return false
}

// render draws one icon of the given pixel size with supersampled coverage.
func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	step := canvas / float64(size) / supersample
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var bg, wh int
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					x := (float64(px)*supersample + float64(sx) + 0.5) * step
					y := (float64(py)*supersample + float64(sy) + 0.5) * step
					if !inRoundedSquare(x, y) {
						continue
					}
					if inWheel(x, y) {
						wh++
					} else {
						bg++
					}
				}
			}
			total := float64(supersample * supersample)
			cov := float64(bg+wh) / total
			if cov == 0 {
				continue
			}
			// Blend white over blue inside the square, then apply coverage alpha.
			t := float64(wh) / float64(bg+wh)
			mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t + 0.5) }
			img.SetNRGBA(px, py, color.NRGBA{
				R: mix(blue.R, white.R),
				G: mix(blue.G, white.G),
				B: mix(blue.B, white.B),
				A: uint8(cov*255 + 0.5),
			})
		}
	}
	return img
}

// writeICO packs the PNG-encoded images into an ICO container
// (ICONDIR + one ICONDIRENTRY per image + raw PNG blobs).
func writeICO(path string, sizes []int) error {
	pngs := make([][]byte, len(sizes))
	for i, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(s)); err != nil {
			return err
		}
		pngs[i] = buf.Bytes()
	}

	var out bytes.Buffer
	w := func(v any) { binary.Write(&out, binary.LittleEndian, v) }
	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(len(sizes)))
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		b := byte(s) // 48→48, 32→32, 16→16 (256 would be 0)
		out.WriteByte(b)
		out.WriteByte(b)
		out.WriteByte(0) // palette colors
		out.WriteByte(0) // reserved
		w(uint16(1))     // color planes
		w(uint16(32))    // bits per pixel
		w(uint32(len(pngs[i])))
		w(uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func main() {
	path := "web/favicon.ico"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	if err := writeICO(path, []int{16, 32, 48}); err != nil {
		fmt.Fprintf(os.Stderr, "gen_favicon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("wrote", path)
}
