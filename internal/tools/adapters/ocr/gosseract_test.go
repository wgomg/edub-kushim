//go:build cgo

package ocr

import "testing"

func TestIsNonFatalStderr(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"pixReadMemTiff pattern", "pixReadMemTiff: function not present", true},
		{"font pixa not made pattern", "font pixa not made", true},
		{"bmfCreate pattern", "bmfCreate failed", true},
		{"case insensitive", "PixReadMemTiff: FUNCTION NOT PRESENT", true},
		{"mixed with other output", "warning: pixReadMemTiff: function not present\nother output", true},
		{"empty string", "", false},
		{"unrelated error", "signal: segmentation fault", false},
		{"no match", "something went wrong", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonFatalStderr(tt.stderr); got != tt.want {
				t.Errorf("isNonFatalStderr(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}
