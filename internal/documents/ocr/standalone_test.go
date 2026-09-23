//go:build cgo

package ocr

import "testing"

func TestWordPlacement(t *testing.T) {
	tests := []struct {
		name              string
		height, bottom    float64
		metrics           fontMetrics
		wantFontSize      float64
		wantBaselineDelta float64 // expected offset added to bottom
	}{
		{
			name:           "valid metrics fill box height",
			height:         12,
			bottom:         50,
			metrics:        fontMetrics{ascent: 1000, descent: -200},
			wantFontSize:   10, // 12 * 1000 / 1200
			wantBaselineDelta: 2, // -(-200)/1000 * 10
		},
		{
			name:           "zero metrics falls back to heuristic",
			height:         10,
			bottom:         50,
			metrics:        fontMetrics{},
			wantFontSize:   8,  // 10 * 0.8
			wantBaselineDelta: 2, // 10 * 0.2
		},
		{
			name:           "tiny box floors font size at 2",
			height:         1,
			bottom:         50,
			metrics:        fontMetrics{ascent: 1000, descent: -200},
			wantFontSize:   2,
			wantBaselineDelta: 0.4, // -(-200)/1000 * 2
		},
		{
			name:           "zero descent leaves baseline at box bottom",
			height:         12,
			bottom:         50,
			metrics:        fontMetrics{ascent: 1000, descent: 0},
			wantFontSize:   12, // 12 * 1000 / 1000
			wantBaselineDelta: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fontSize, baseline := wordPlacement(tt.height, tt.bottom, tt.metrics)
			if fontSize != tt.wantFontSize {
				t.Errorf("fontSize = %v, want %v", fontSize, tt.wantFontSize)
			}
			wantBaseline := tt.bottom + tt.wantBaselineDelta
			if baseline != wantBaseline {
				t.Errorf("baseline = %v, want %v", baseline, wantBaseline)
			}
		})
	}
}

func TestStretchScale(t *testing.T) {
	tests := []struct {
		name                string
		boxWidth, natural   float64
		want                float64
	}{
		{"in-range 150%", 30, 20, 150},
		{"lower boundary 25% inclusive", 5, 20, 25},
		{"below lower clamp returns 0", 4, 20, 0},
		{"upper boundary 400% inclusive", 80, 20, 400},
		{"above upper clamp returns 0", 100, 20, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stretchScale(tt.boxWidth, tt.natural); got != tt.want {
				t.Errorf("stretchScale(%v, %v) = %v, want %v",
					tt.boxWidth, tt.natural, got, tt.want)
			}
		})
	}
}