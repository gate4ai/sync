package trayapp

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// errorBadge overlays a red exclamation mark on the bottom-right corner of
// the base tray icon, so the icon stays recognizable while making a sync
// failure visible without the user having to open the menu. It is computed
// once at startup rather than shipped as a second embedded asset — one
// source image is easier to keep in sync than two.
func errorBadge(basePNG []byte) ([]byte, error) {
	base, err := png.Decode(bytes.NewReader(basePNG))
	if err != nil {
		return nil, err
	}
	bounds := base.Bounds()
	out := image.NewNRGBA(bounds)
	draw.Draw(out, bounds, base, bounds.Min, draw.Src)

	size := bounds.Dx()
	cx, cy := float64(size)*0.72, float64(size)*0.72
	radius := float64(size) * 0.30

	red := color.NRGBA{0xd3, 0x2f, 0x2f, 0xff}
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			dist := dx*dx + dy*dy
			if dist > radius*radius {
				continue
			}
			out.Set(x, y, red)
			if isExclamationMark(dx, dy, radius) {
				out.Set(x, y, white)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// isExclamationMark reports whether the point at offset (dx, dy) from the
// badge's center falls on the "!" glyph: a stem in the upper portion of the
// circle and a separate dot below it.
func isExclamationMark(dx, dy, radius float64) bool {
	stemHalfWidth := radius * 0.14
	if dx < -stemHalfWidth || dx > stemHalfWidth {
		return false
	}
	if dy >= -radius*0.55 && dy <= radius*0.05 {
		return true // stem
	}
	dotCenter := radius * 0.35
	dotRadius := radius * 0.16
	ddy := dy - dotCenter
	return ddy*ddy+dx*dx <= dotRadius*dotRadius // dot
}
