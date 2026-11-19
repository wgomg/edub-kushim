//go:build cgo

package thumbnail

import (
	"image"
	"strings"
	"testing"
)

// TestCoverFit pins the sampling bounds of coverFit: geometries whose cover window reaches the final
// source pixel must succeed, and a short buffer must be rejected by the entry dimension check.
func TestCoverFit(t *testing.T) {
	const tw, th = 400, 533

	cases := []struct {
		name string
		w, h int
	}{
		{"cover window reaches last pixel 300x400", 300, 400},
		{"target box size 400x533", 400, 533},
		{"near 3:4 mid size 350x467", 350, 467},
		{"tiny 2x2", 2, 2},
		{"tiny 3x4", 3, 4},
		{"us letter 612x792", 612, 792},
		{"a4 595x842", 595, 842},
		{"small 200x300", 200, 300},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			samples := make([]byte, tc.w*tc.h*3)
			for i := range samples {
				samples[i] = byte(i)
			}

			img, err := coverFit(samples, tc.w, tc.h, tw, th)
			if err != nil {
				t.Fatalf("coverFit(%dx%d): unexpected error: %v", tc.w, tc.h, err)
			}
			if got, want := img.Bounds(), image.Rect(0, 0, tw, th); got != want {
				t.Fatalf("coverFit(%dx%d): bounds = %v, want %v", tc.w, tc.h, got, want)
			}
			for i := 3; i < len(img.Pix); i += 4 {
				if img.Pix[i] != 0xff {
					t.Fatalf("coverFit(%dx%d): pixel at byte %d has alpha %d, want 255", tc.w, tc.h, i, img.Pix[i])
				}
			}
		})
	}

	t.Run("short buffer is rejected", func(t *testing.T) {
		samples := make([]byte, 10*10*3-1)
		_, err := coverFit(samples, 10, 10, tw, th)
		if err == nil {
			t.Fatal("coverFit with a short sample buffer: expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid source dimensions") {
			t.Fatalf("coverFit with a short sample buffer: error = %v, want invalid source dimensions", err)
		}
	})
}
