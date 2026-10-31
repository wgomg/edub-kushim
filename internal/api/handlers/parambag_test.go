package handlers

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParamBag_GetFallsBackToDefault(t *testing.T) {
	pb := NewParamBag(httptest.NewRequest("GET", "/x?q=hello", nil))

	if got := pb.Get("q", "fallback"); got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
	if got := pb.Get("missing", "fallback"); got != "fallback" {
		t.Errorf("expected fallback for absent key, got %q", got)
	}
}

func TestParamBag_GetIntParsesAndClamps(t *testing.T) {
	pb := NewParamBag(httptest.NewRequest("GET", "/x?limit=10", nil))

	if got := pb.GetInt("limit", 50, 1, 100); got != 10 {
		t.Errorf("expected 10, got %d", got)
	}
	if got := pb.GetInt("absent", 50, 1, 100); got != 50 {
		t.Errorf("expected default 50 for absent key, got %d", got)
	}
	if got := pb.GetInt("limit", 50, 20, 100); got != 50 {
		t.Errorf("expected default when below min, got %d", got)
	}
	if got := pb.GetInt("limit", 50, 1, 5); got != 50 {
		t.Errorf("expected default when above max, got %d", got)
	}
}

func TestParamBag_GetIntRejectsMalformedValue(t *testing.T) {
	pb := NewParamBag(httptest.NewRequest("GET", "/x?limit=abc", nil))

	if got := pb.GetInt("limit", 50, 1, 100); got != 50 {
		t.Errorf("expected default 50 for malformed value, got %d", got)
	}
}

func TestParamBag_GetInt64Parses(t *testing.T) {
	pb := NewParamBag(httptest.NewRequest("GET", "/x?offset=4294967296", nil))

	if got := pb.GetInt64("offset", 0, 0, 1<<40); got != 4294967296 {
		t.Errorf("expected 4294967296, got %d", got)
	}
}

func TestParamBag_GetBoolParsesAndRejectsMalformed(t *testing.T) {
	pb := NewParamBag(httptest.NewRequest("GET", "/x?flag=true&broken=maybe", nil))

	if got := pb.GetBool("flag", false); !got {
		t.Error("expected true for true")
	}
	if got := pb.GetBool("broken", true); !got {
		t.Error("expected default true for malformed value")
	}
}

func TestParamBag_GetStringsSplitsCommaSeparatedValues(t *testing.T) {
	pb := NewParamBag(httptest.NewRequest("GET", "/x?size=big,small", nil))

	if got := pb.GetStrings("size"); !reflect.DeepEqual(got, []string{"big", "small"}) {
		t.Errorf("expected comma-separated values in order, got %v", got)
	}
	if got := pb.GetStrings("absent"); got != nil {
		t.Errorf("expected nil for absent key, got %v", got)
	}
}
