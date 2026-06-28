package config

import (
	"testing"
)

func TestParseHHMM_Valid(t *testing.T) {
	tests := []struct {
		input    string
		allowEnd bool
		want     int
	}{
		{"00:00", false, 0},
		{"09:30", false, 570},
		{"12:00", false, 720},
		{"23:59", false, 1439},
		{"24:00", true, 1440},
		{"01:05", false, 65},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseHHMM(tt.input, tt.allowEnd)
			if err != nil {
				t.Fatalf("parseHHMM(%q, %v) unexpected error: %v", tt.input, tt.allowEnd, err)
			}
			if got != tt.want {
				t.Errorf("parseHHMM(%q, %v) = %d, want %d", tt.input, tt.allowEnd, got, tt.want)
			}
		})
	}
}

func TestParseHHMM_Invalid(t *testing.T) {
	tests := []struct {
		input    string
		allowEnd bool
	}{
		{"25:00", false},
		{"12:60", false},
		{"abc", false},
		{"", false},
		{"24:00", false},
		{"24:01", true},
		{"9:30", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseHHMM(tt.input, tt.allowEnd)
			if err == nil {
				t.Fatalf("parseHHMM(%q, %v) expected error, got nil", tt.input, tt.allowEnd)
			}
		})
	}
}

func TestValidatePollingWindows_Valid(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"no key", map[string]any{}},
		{"empty array", map[string]any{"consumer.polling.windows": []any{}}},
		{"single window", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "06:00"},
			},
		}},
		{"multiple windows", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "06:00"},
				map[string]any{"start": "14:00", "end": "16:00"},
			},
		}},
		{"24:00 end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "22:00", "end": "24:00"},
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePollingWindows(tt.body); err != nil {
				t.Fatalf("ValidatePollingWindows() unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePollingWindows_Invalid(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"nil value", map[string]any{"consumer.polling.windows": nil}},
		{"wrong type", map[string]any{"consumer.polling.windows": "not-array"}},
		{"non-object entry", map[string]any{
			"consumer.polling.windows": []any{"bad"},
		}},
		{"missing start", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"end": "06:00"},
			},
		}},
		{"missing end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00"},
			},
		}},
		{"invalid start format", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "25:00", "end": "06:00"},
			},
		}},
		{"invalid end format", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "25:00"},
			},
		}},
		{"end is 00:00", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "00:00"},
			},
		}},
		{"start equals end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "02:00"},
			},
		}},
		{"start after end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "06:00", "end": "02:00"},
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePollingWindows(tt.body); err == nil {
				t.Fatal("ValidatePollingWindows() expected error, got nil")
			}
		})
	}
}

func TestIsWithinActiveWindows_EmptyWindows(t *testing.T) {
	if !IsWithinActiveWindows(nil) {
		t.Error("IsWithinActiveWindows(nil) = false, want true")
	}
	if !IsWithinActiveWindows([]PollingWindow{}) {
		t.Error("IsWithinActiveWindows([]) = false, want true")
	}
}
