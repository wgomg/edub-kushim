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

// TestParamBag_GetStringsJoinsRepeatedKeys pins the array encoding the generated API spec declares for its
// GetStrings parameters: repeated keys split on commas in arrival order, so ?size=big&size=small returns the
// same entries as ?size=big,small, empty entries included; an absent key or a single empty value returns nil,
// and scalars keep reading the first value of the key.
func TestParamBag_GetStringsJoinsRepeatedKeys(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "csv value", query: "/x?size=big,small", want: []string{"big", "small"}},
		{name: "repeated key", query: "/x?size=big&size=small", want: []string{"big", "small"}},
		{name: "mixed encodings", query: "/x?size=big,small&size=tiny", want: []string{"big", "small", "tiny"}},
		{name: "absent key", query: "/x?other=1", want: nil},
		{name: "single empty value", query: "/x?size=", want: nil},
		{name: "multiple empty values", query: "/x?size=&size=", want: []string{"", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pb := NewParamBag(httptest.NewRequest("GET", tc.query, nil))

			if got := pb.GetStrings("size"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GetStrings(size) = %v, want %v", got, tc.want)
			}
		})
	}

	pb := NewParamBag(httptest.NewRequest("GET", "/x?limit=1&limit=2", nil))
	if got := pb.GetInt("limit", 50, 1, 100); got != 1 {
		t.Errorf("GetInt(limit) = %d, want the first value 1", got)
	}
}
