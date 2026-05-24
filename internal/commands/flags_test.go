package commands

import (
	"testing"
)

func TestNewFlagParser(t *testing.T) {
	p := NewFlagParser([]string{"--foo", "bar"})
	if p == nil {
		t.Fatal("expected non-nil parser")
	}
}

func TestHelp_HelpFlag(t *testing.T) {
	p := NewFlagParser([]string{"--help"})
	if !p.Help("usage text") {
		t.Error("expected Help to return true for --help")
	}
}

func TestHelp_ShortHelpFlag(t *testing.T) {
	p := NewFlagParser([]string{"-h"})
	if !p.Help("usage text") {
		t.Error("expected Help to return true for -h")
	}
}

func TestHelp_NoHelpFlag(t *testing.T) {
	p := NewFlagParser([]string{"--other"})
	if p.Help("usage text") {
		t.Error("expected Help to return false")
	}
}

func TestString_ParsesValue(t *testing.T) {
	p := NewFlagParser([]string{"--name", "test-value"})
	var dst string
	if err := p.String("--name", &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst != "test-value" {
		t.Errorf("expected test-value, got %s", dst)
	}
}

func TestString_MissingValue(t *testing.T) {
	p := NewFlagParser([]string{"--name"})
	var dst string
	if err := p.String("--name", &dst); err == nil {
		t.Error("expected error for missing value")
	}
}

func TestString_NotFound(t *testing.T) {
	p := NewFlagParser([]string{"--other", "val"})
	var dst string
	if err := p.String("--name", &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst != "" {
		t.Errorf("expected empty, got %s", dst)
	}
}

func TestInt_ParsesValue(t *testing.T) {
	p := NewFlagParser([]string{"--count", "5"})
	var dst int
	if err := p.Int("--count", &dst, 0, 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst != 5 {
		t.Errorf("expected 5, got %d", dst)
	}
}

func TestInt_OutOfRange(t *testing.T) {
	p := NewFlagParser([]string{"--count", "15"})
	var dst int
	if err := p.Int("--count", &dst, 0, 10); err == nil {
		t.Error("expected error for out of range")
	}
}

func TestInt_InvalidValue(t *testing.T) {
	p := NewFlagParser([]string{"--count", "abc"})
	var dst int
	if err := p.Int("--count", &dst, 0, 10); err == nil {
		t.Error("expected error for invalid integer")
	}
}

func TestInt_NotFound(t *testing.T) {
	p := NewFlagParser([]string{"--other", "5"})
	var dst int
	if err := p.Int("--count", &dst, 0, 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst != 0 {
		t.Errorf("expected 0, got %d", dst)
	}
}

func TestBool_SetsTrue(t *testing.T) {
	p := NewFlagParser([]string{"--verbose"})
	var dst bool
	if err := p.Bool("--verbose", &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dst {
		t.Error("expected true")
	}
}

func TestBool_NotFound(t *testing.T) {
	p := NewFlagParser([]string{"--other"})
	var dst bool
	if err := p.Bool("--verbose", &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst {
		t.Error("expected false")
	}
}

func TestRest_ReturnsRemaining(t *testing.T) {
	p := NewFlagParser([]string{"--foo", "bar", "extra1", "extra2"})
	var dst string
	p.String("--foo", &dst)
	rest := p.Rest()
	if len(rest) != 2 || rest[0] != "extra1" || rest[1] != "extra2" {
		t.Errorf("unexpected rest: %v", rest)
	}
}

func TestRest_Idempotent(t *testing.T) {
	p := NewFlagParser([]string{"a", "b"})
	r1 := p.Rest()
	r2 := p.Rest()
	if len(r1) != 2 || len(r2) != 2 {
		t.Error("expected same length on both calls")
	}
}

func TestString_AdvancesPosition(t *testing.T) {
	p := NewFlagParser([]string{"--first", "a", "--second", "b"})
	var first, second string
	p.String("--first", &first)
	p.String("--second", &second)
	if first != "a" || second != "b" {
		t.Errorf("unexpected: first=%s second=%s", first, second)
	}
}
