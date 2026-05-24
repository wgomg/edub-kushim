package utils

import (
	"net/http/httptest"
	"testing"
)

func TestNewParamBag(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?foo=bar&num=42", nil)
	pb := NewParamBag(req)
	if pb == nil {
		t.Fatal("expected non-nil param bag")
	}
	if pb.Get("foo", "") != "bar" {
		t.Errorf("foo = %q", pb.Get("foo", ""))
	}
}

func TestGet_DefaultValue(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	got := pb.Get("missing", "def")
	if got != "def" {
		t.Errorf("got %q, want %q", got, "def")
	}
}

func TestGet_PathValue(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	pb.SetPathValue("id", "99")
	got := pb.Get("id", "")
	if got != "99" {
		t.Errorf("got %q, want %q", got, "99")
	}
}

func TestGet_PathOverQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?id=query-val", nil)
	pb := NewParamBag(req)
	pb.SetPathValue("id", "path-val")
	got := pb.Get("id", "")

	if got != "query-val" {
		t.Errorf("got %q, want %q", got, "query-val")
	}
}

func TestGetInt_Valid(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=42", nil)
	pb := NewParamBag(req)
	got := pb.GetInt("n", 0, 1, 100)
	if got != 42 {
		t.Errorf("got %d, want %d", got, 42)
	}
}

func TestGetInt_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	got := pb.GetInt("n", 10, 1, 100)
	if got != 10 {
		t.Errorf("got %d, want %d", got, 10)
	}
}

func TestGetInt_BelowMin(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=0", nil)
	pb := NewParamBag(req)
	got := pb.GetInt("n", 10, 1, 100)
	if got != 10 {
		t.Errorf("got %d, want %d", got, 10)
	}
}

func TestGetInt_AboveMax(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=200", nil)
	pb := NewParamBag(req)
	got := pb.GetInt("n", 10, 1, 100)
	if got != 10 {
		t.Errorf("got %d, want %d", got, 10)
	}
}

func TestGetInt_InvalidFormat(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=abc", nil)
	pb := NewParamBag(req)
	got := pb.GetInt("n", 5, 1, 100)
	if got != 5 {
		t.Errorf("got %d, want %d", got, 5)
	}
}

func TestGetInt_NoBounds(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=42", nil)
	pb := NewParamBag(req)
	got := pb.GetInt("n", 0, 0, 0)
	if got != 42 {
		t.Errorf("got %d, want %d", got, 42)
	}
}

func TestGetInt64_Valid(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=99", nil)
	pb := NewParamBag(req)
	got := pb.GetInt64("n", 0, 1, 100)
	if got != 99 {
		t.Errorf("got %d, want %d", got, 99)
	}
}

func TestGetInt64_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	got := pb.GetInt64("n", 50, 1, 100)
	if got != 50 {
		t.Errorf("got %d, want %d", got, 50)
	}
}

func TestGetInt64_BelowMin(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?n=0", nil)
	pb := NewParamBag(req)
	got := pb.GetInt64("n", 10, 1, 100)
	if got != 10 {
		t.Errorf("got %d, want %d", got, 10)
	}
}

func TestGetBool_True(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?flag=true", nil)
	pb := NewParamBag(req)
	if !pb.GetBool("flag", false) {
		t.Error("expected true")
	}
}

func TestGetBool_False(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?flag=false", nil)
	pb := NewParamBag(req)
	if pb.GetBool("flag", true) {
		t.Error("expected false")
	}
}

func TestGetBool_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	if !pb.GetBool("flag", true) {
		t.Error("expected default true")
	}
}

func TestGetBool_Invalid(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?flag=yes", nil)
	pb := NewParamBag(req)
	if pb.GetBool("flag", false) {
		t.Error("expected default false for invalid input")
	}
}

func TestGetStrings_Single(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?tags=go", nil)
	pb := NewParamBag(req)
	got := pb.GetStrings("tags")
	if len(got) != 1 || got[0] != "go" {
		t.Errorf("got %v, want [go]", got)
	}
}

func TestGetStrings_Multiple(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?tags=a,b,c", nil)
	pb := NewParamBag(req)
	got := pb.GetStrings("tags")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("got %v, want [a b c]", got)
	}
}

func TestGetStrings_Empty(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	got := pb.GetStrings("tags")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestGetStrings_EmptyString(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?tags=", nil)
	pb := NewParamBag(req)
	got := pb.GetStrings("tags")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestWithParamBagAndGetParamBag(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	pb := NewParamBag(req)
	pb.SetPathValue("k", "v")

	req = WithParamBag(req, pb)
	got := GetParamBag(req)
	if got == nil {
		t.Fatal("GetParamBag returned nil")
	}
	if got.Get("k", "") != "v" {
		t.Errorf("k = %q", got.Get("k", ""))
	}
}

func TestGetParamBag_NoBag(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	if pb := GetParamBag(req); pb != nil {
		t.Error("expected nil when no bag in context")
	}
}
