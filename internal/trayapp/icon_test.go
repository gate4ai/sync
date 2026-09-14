package trayapp

import (
	"bytes"
	"image/png"
	"testing"
)

func TestErrorBadgeProducesAValidPNGTheSameSize(t *testing.T) {
	base, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		t.Fatalf("decode base icon: %v", err)
	}

	badged, err := errorBadge(iconPNG)
	if err != nil {
		t.Fatalf("errorBadge: %v", err)
	}
	out, err := png.Decode(bytes.NewReader(badged))
	if err != nil {
		t.Fatalf("decode badged icon: %v", err)
	}
	if out.Bounds() != base.Bounds() {
		t.Errorf("badged bounds = %v, want %v", out.Bounds(), base.Bounds())
	}
}

func TestErrorBadgeDiffersFromTheBaseIcon(t *testing.T) {
	badged, err := errorBadge(iconPNG)
	if err != nil {
		t.Fatalf("errorBadge: %v", err)
	}
	if bytes.Equal(badged, iconPNG) {
		t.Error("badged icon is byte-identical to the base icon")
	}
}
