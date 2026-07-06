//go:build cgo

package ocr

import (
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
