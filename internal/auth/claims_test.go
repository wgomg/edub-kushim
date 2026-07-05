package auth

import "testing"

func TestValidRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"admin", true},
		{"editor", true},
		{"viewer", true},
		{"superadmin", false},
		{"", false},
		{"Admin", false},
		{"VIEWER", false},
	}
	for _, tt := range tests {
		if got := ValidRole(tt.role); got != tt.want {
			t.Errorf("ValidRole(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}
