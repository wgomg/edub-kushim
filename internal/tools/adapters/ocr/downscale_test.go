//go:build cgo

package ocr

import (
	"image"
	"image/color"
	"testing"
)

func TestDownscaleRGB_ExactRatio(t *testing.T) {
	inW, inH := 4, 3
	outW, outH := 2, 2

	in := make([]byte, inW*inH*3)
	for i := range in {
		in[i] = byte(i % 256)
	}

	out := downscaleRGB(in, inW, inH, outW, outH)

	if len(out) != outW*outH*3 {
		t.Fatalf("output length = %d, want %d", len(out), outW*outH*3)
	}

	for y := range outH {
		for x := range outW {
			si := (y*inH/outH*inW + x*inW/outW) * 3
			di := (y*outW + x) * 3
			if out[di] != in[si] || out[di+1] != in[si+1] || out[di+2] != in[si+2] {
				t.Errorf("pixel (%d,%d): got [%d %d %d], want [%d %d %d]",
					x, y, out[di], out[di+1], out[di+2], in[si], in[si+1], in[si+2])
			}
		}
	}
}

func TestDownscaleRGB_ScaleDown200to150(t *testing.T) {
	inW, inH := 200, 150
	outW := inW * 150 / 200 // 150
	outH := inH * 150 / 200 // 112

	in := make([]byte, inW*inH*3)
	for i := range in {
		in[i] = byte(i % 256)
	}

	out := downscaleRGB(in, inW, inH, outW, outH)

	if len(out) != outW*outH*3 {
		t.Fatalf("output length = %d, want %d", len(out), outW*outH*3)
	}
}

func TestDownscaleRGB_Identity(t *testing.T) {
	w, h := 5, 5
	in := make([]byte, w*h*3)
	for i := range in {
		in[i] = byte(i % 256)
	}

	out := downscaleRGB(in, w, h, w, h)

	if len(out) != len(in) {
		t.Fatalf("output length = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("byte %d: got %d, want %d", i, out[i], in[i])
		}
	}
}

func TestImageToRGB_Opaque(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	samples := imageToRGB(img)

	if len(samples) != 2*2*3 {
		t.Fatalf("expected %d bytes, got %d", 2*2*3, len(samples))
	}

	for i := range 4 {
		r, g, b := samples[i*3], samples[i*3+1], samples[i*3+2]
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("pixel %d: got [%d %d %d], want [255 0 0]", i, r, g, b)
		}
	}
}

func TestImageToRGB_Transparent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 0, G: 0, B: 0, A: 0})

	samples := imageToRGB(img)

	if samples[0] != 255 || samples[1] != 255 || samples[2] != 255 {
		t.Errorf("transparent pixel should be white, got [%d %d %d]", samples[0], samples[1], samples[2])
	}
}

func TestImageToRGB_SemiTransparent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 128})

	samples := imageToRGB(img)

	// Semi-transparent red (50% alpha) on white background: pink (255, 127, 127)
	if samples[0] != 255 || samples[1] != 127 || samples[2] != 127 {
		t.Errorf("semi-transparent red on white: got [%d %d %d], want [255 127 127]", samples[0], samples[1], samples[2])
	}
}
