package utils

import (
	"runtime"
	"testing"
)

func TestReadMemSnapshot(t *testing.T) {
	runtime.GC()
	s := ReadMemSnapshot()
	if s.HeapInUse == 0 {
		t.Error("expected HeapInUse > 0")
	}
	if s.HeapAlloc == 0 {
		t.Error("expected HeapAlloc > 0")
	}
	if s.RSS == 0 {
		t.Error("expected RSS > 0")
	}
}

func TestFormatMemDelta_positive(t *testing.T) {
	before := MemSnapshot{HeapInUse: 1000, RSS: 2000, NumGC: 5}
	after := MemSnapshot{HeapInUse: 2000, RSS: 4000, NumGC: 7}
	got := FormatMemDelta(before, after)
	if got != "heap +1000 B, RSS +2.0 KiB, 2 GC(s)" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestFormatMemDelta_negative(t *testing.T) {
	before := MemSnapshot{HeapInUse: 5000, RSS: 10000, NumGC: 10}
	after := MemSnapshot{HeapInUse: 2000, RSS: 4000, NumGC: 10}
	got := FormatMemDelta(before, after)
	if got != "heap -2.9 KiB, RSS -5.9 KiB" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestFormatMemDelta_zeroGC(t *testing.T) {
	before := MemSnapshot{HeapInUse: 1000, RSS: 2000, NumGC: 3}
	after := MemSnapshot{HeapInUse: 1500, RSS: 2500, NumGC: 3}
	got := FormatMemDelta(before, after)
	if got != "heap +500 B, RSS +500 B" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input uint64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %s; want %s", tt.input, got, tt.want)
		}
	}
}
