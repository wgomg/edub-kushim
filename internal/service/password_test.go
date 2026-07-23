package service

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/errs"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		pass    string
		wantErr bool
		kind    errs.Kind
	}{
		{"valid password", "MyP@ssw0rd123", false, 0},
		{"valid with special chars", "aB3!@#$%^&*()_", false, 0},
		{"empty password", "", true, errs.KindInvalid},
		{"too short", "Ab1!short", true, errs.KindInvalid},
		{"exactly 11 chars", "Abcdef1!xyz", true, errs.KindInvalid},
		{"exactly 12 chars", "Abcdef1!xyz9", false, 0},
		{"too long", string(make([]byte, 129)), true, errs.KindInvalid},
		{"no uppercase", "alllower1!case", true, errs.KindInvalid},
		{"no lowercase", "ALLUPPER1!CASE", true, errs.KindInvalid},
		{"no digit", "NoDigit!xyzw", true, errs.KindInvalid},
		{"no special char", "NoSpecial123", true, errs.KindInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.pass)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.wantErr && errs.KindOf(err) != tt.kind {
				t.Errorf("error kind = %d, want %d", errs.KindOf(err), tt.kind)
			}
		})
	}
}
