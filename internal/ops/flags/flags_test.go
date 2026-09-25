package flags

import (
	"strings"
	"testing"
)

func TestFlagParserString(t *testing.T) {
	t.Run("reads the value and consumes the pair", func(t *testing.T) {
		p := NewFlagParser([]string{"--batch", "abc", "extra"})
		var got string
		if err := p.String("--batch", &got); err != nil {
			t.Fatalf("String: %v", err)
		}
		if got != "abc" {
			t.Errorf("value = %q, want %q", got, "abc")
		}
		if rest := p.Rest(); len(rest) != 1 || rest[0] != "extra" {
			t.Errorf("Rest() = %v, want [extra]", rest)
		}
	})

	t.Run("flag without a value errors", func(t *testing.T) {
		p := NewFlagParser([]string{"list", "--batch"})
		var got string
		err := p.String("--batch", &got)
		if err == nil || !strings.Contains(err.Error(), "requires a value") {
			t.Fatalf("String error = %v, want a missing-value error", err)
		}
	})

	t.Run("repeated occurrences are read in order", func(t *testing.T) {
		p := NewFlagParser([]string{"--batch", "first", "--batch", "second"})
		var first, second string
		if err := p.String("--batch", &first); err != nil {
			t.Fatalf("first String: %v", err)
		}
		if err := p.String("--batch", &second); err != nil {
			t.Fatalf("second String: %v", err)
		}
		if first != "first" || second != "second" {
			t.Errorf("values = %q, %q, want \"first\", \"second\"", first, second)
		}
	})

	t.Run("an argument taken as another flag's value cannot satisfy a second flag", func(t *testing.T) {
		p := NewFlagParser([]string{"--batch", "--force"})
		var batch string
		if err := p.String("--batch", &batch); err != nil {
			t.Fatalf("String: %v", err)
		}
		if batch != "--force" {
			t.Errorf("value = %q, want %q", batch, "--force")
		}
		var force bool
		if err := p.Bool("--force", &force); err != nil {
			t.Fatalf("Bool: %v", err)
		}
		if force {
			t.Error("--force parsed as a flag although it was already consumed as --batch's value")
		}
	})
}

func TestFlagParserInt(t *testing.T) {
	t.Run("within the bounds", func(t *testing.T) {
		p := NewFlagParser([]string{"--limit", "5"})
		var got int
		if err := p.Int("--limit", &got, 1, 10); err != nil {
			t.Fatalf("Int: %v", err)
		}
		if got != 5 {
			t.Errorf("value = %d, want 5", got)
		}
	})

	t.Run("outside the bounds errors", func(t *testing.T) {
		p := NewFlagParser([]string{"--limit", "11"})
		var got int
		err := p.Int("--limit", &got, 1, 10)
		if err == nil || !strings.Contains(err.Error(), "between 1 and 10") {
			t.Fatalf("Int error = %v, want a bounds error", err)
		}
	})

	t.Run("non-integer errors", func(t *testing.T) {
		p := NewFlagParser([]string{"--limit", "abc"})
		var got int
		err := p.Int("--limit", &got, 1, 10)
		if err == nil || !strings.Contains(err.Error(), "invalid integer") {
			t.Fatalf("Int error = %v, want an invalid-integer error", err)
		}
	})

	t.Run("valueless flag leaves the default", func(t *testing.T) {
		p := NewFlagParser([]string{"--limit"})
		got := 7
		if err := p.Int("--limit", &got, 1, 10); err != nil {
			t.Fatalf("Int: %v", err)
		}
		if got != 7 {
			t.Errorf("value = %d, want the untouched default 7", got)
		}
	})
}

func TestFlagParserBool(t *testing.T) {
	t.Run("present sets the flag and consumes only itself", func(t *testing.T) {
		p := NewFlagParser([]string{"--force", "x"})
		var force bool
		if err := p.Bool("--force", &force); err != nil {
			t.Fatalf("Bool: %v", err)
		}
		if !force {
			t.Error("force = false, want true")
		}
		if rest := p.Rest(); len(rest) != 1 || rest[0] != "x" {
			t.Errorf("Rest() = %v, want [x]", rest)
		}
	})

	t.Run("absent leaves the flag false", func(t *testing.T) {
		p := NewFlagParser([]string{"list"})
		var force bool
		if err := p.Bool("--force", &force); err != nil {
			t.Fatalf("Bool: %v", err)
		}
		if force {
			t.Error("force = true, want false")
		}
	})
}

func TestFlagParserHelp(t *testing.T) {
	if !NewFlagParser([]string{"list", "--help"}).Help("usage text") {
		t.Error("--help not reported as present")
	}
	if !NewFlagParser([]string{"-h"}).Help("usage text") {
		t.Error("-h not reported as present")
	}
	if NewFlagParser([]string{"list"}).Help("usage text") {
		t.Error("Help reported a short-circuit for an argument list without a help flag")
	}
}
