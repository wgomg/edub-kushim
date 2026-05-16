package commands

import (
	"testing"
)

func TestFlagParserString(t *testing.T) {
	var s string
	p := NewFlagParser([]string{"--limit", "20", "query"})
	err := p.String("--limit", &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "20" {
		t.Errorf("expected '20', got %q", s)
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "query" {
		t.Errorf("expected rest ['query'], got %v", rest)
	}
}

func TestFlagParserStringMissingValue(t *testing.T) {
	var s string
	p := NewFlagParser([]string{"--limit"})
	err := p.String("--limit", &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}

func TestFlagParserInt(t *testing.T) {
	var i int
	p := NewFlagParser([]string{"--limit", "20", "query"})
	err := p.Int("--limit", &i, 1, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if i != 20 {
		t.Errorf("expected 20, got %d", i)
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "query" {
		t.Errorf("expected rest ['query'], got %v", rest)
	}
}

func TestFlagParserIntOutOfRange(t *testing.T) {
	var i int
	p := NewFlagParser([]string{"--limit", "0"})
	err := p.Int("--limit", &i, 1, 100)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFlagParserIntNotANumber(t *testing.T) {
	var i int
	p := NewFlagParser([]string{"--limit", "abc"})
	err := p.Int("--limit", &i, 1, 100)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFlagParserBool(t *testing.T) {
	var b bool
	p := NewFlagParser([]string{"--rebuild-index", "query"})
	err := p.Bool("--rebuild-index", &b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !b {
		t.Error("expected true")
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "query" {
		t.Errorf("expected rest ['query'], got %v", rest)
	}
}

func TestFlagParserMultipleFlags(t *testing.T) {
	var limit int
	var offset int
	p := NewFlagParser([]string{"--limit", "10", "--offset", "5", "query"})
	if err := p.Int("--limit", &limit, 1, 100); err != nil {
		t.Fatalf("limit: %v", err)
	}
	if err := p.Int("--offset", &offset, 0, 100); err != nil {
		t.Fatalf("offset: %v", err)
	}
	if limit != 10 {
		t.Errorf("expected limit 10, got %d", limit)
	}
	if offset != 5 {
		t.Errorf("expected offset 5, got %d", offset)
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "query" {
		t.Errorf("expected rest ['query'], got %v", rest)
	}
}

func TestFlagParserNoFlags(t *testing.T) {
	p := NewFlagParser([]string{"query", "terms"})
	rest := p.Rest()
	if len(rest) != 2 || rest[0] != "query" || rest[1] != "terms" {
		t.Errorf("expected rest ['query', 'terms'], got %v", rest)
	}
}

func TestFlagParserUnknownFlagInRest(t *testing.T) {
	p := NewFlagParser([]string{"--unknown", "val"})
	rest := p.Rest()
	if len(rest) != 2 || rest[0] != "--unknown" || rest[1] != "val" {
		t.Errorf("expected rest ['--unknown', 'val'], got %v", rest)
	}
}

func TestFlagParserIntMissingValue(t *testing.T) {
	var i int
	p := NewFlagParser([]string{"--limit"})
	err := p.Int("--limit", &i, 1, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if i != 0 {
		t.Errorf("expected 0 (unchanged), got %d", i)
	}
}

func TestFlagParserFlagValueIsAnotherFlag(t *testing.T) {
	var i int
	p := NewFlagParser([]string{"--limit", "--offset", "query"})
	err := p.Int("--limit", &i, 1, 100)
	if err == nil {
		t.Fatal("expected error for non-integer value")
	}
}

func TestFlagParserRestAfterConsumingAll(t *testing.T) {
	var s string
	p := NewFlagParser([]string{"--flag", "value"})
	if err := p.String("--flag", &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rest := p.Rest()
	if len(rest) != 0 {
		t.Errorf("expected empty rest, got %v", rest)
	}
}

func TestFlagParserRestBeforeConsuming(t *testing.T) {
	p := NewFlagParser([]string{"a", "b", "c"})
	rest := p.Rest()
	if len(rest) != 3 {
		t.Errorf("expected 3 items, got %d", len(rest))
	}
	if p.pos != 0 {
		t.Error("pos should not advance on Rest()")
	}
	rest2 := p.Rest()
	if len(rest2) != 3 {
		t.Error("Rest() should be idempotent")
	}
}

func TestFlagParserBoolNotPresent(t *testing.T) {
	var b bool
	p := NewFlagParser([]string{"query"})
	err := p.Bool("--verbose", &b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b {
		t.Error("expected false for missing bool flag")
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "query" {
		t.Errorf("expected rest ['query'], got %v", rest)
	}
}

func TestFlagParserInterleavedFlagsAndRest(t *testing.T) {
	var s string
	var b bool
	p := NewFlagParser([]string{"--flag", "val", "rest1", "--bool", "rest2"})
	if err := p.String("--flag", &s); err != nil {
		t.Fatalf("string: %v", err)
	}
	if s != "val" {
		t.Errorf("expected 'val', got %q", s)
	}
	if err := p.Bool("--bool", &b); err != nil {
		t.Fatalf("bool: %v", err)
	}
	if !b {
		t.Error("expected true")
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "rest2" {
		t.Errorf("expected rest ['rest2'], got %v", rest)
	}
}

func TestFlagParserEmptyArgs(t *testing.T) {
	p := NewFlagParser([]string{})
	rest := p.Rest()
	if len(rest) != 0 {
		t.Errorf("expected empty rest, got %v", rest)
	}
}

func TestFlagParserFlagAtEndNoValue(t *testing.T) {
	var i int
	p := NewFlagParser([]string{"--limit"})
	err := p.Int("--limit", &i, 1, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if i != 0 {
		t.Errorf("expected 0 (unchanged), got %d", i)
	}
	rest := p.Rest()
	if len(rest) != 1 || rest[0] != "--limit" {
		t.Errorf("expected rest ['--limit'], got %v", rest)
	}
}
