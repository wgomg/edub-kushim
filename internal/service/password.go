package service

import (
	"errors"
	"strings"
	"unicode"

	"github.com/wgomg/edub-kushim/internal/errs"
)

const (
	minPasswordLength = 12
	maxPasswordLength = 128
)

// ValidatePassword enforces the password policy: at least twelve and at
// most 128 characters, with at least one uppercase letter, one lowercase
// letter, one digit and one special character. An invalid password is an
// errs invalid-field error.
func ValidatePassword(password string) error {
	if password == "" {
		return errs.EInvalid("password", errors.New("password is required"))
	}
	if len(password) > maxPasswordLength {
		return errs.EInvalid("password", errors.New("password exceeds maximum length"))
	}
	if len(password) < minPasswordLength {
		return errs.EInvalid("password", errors.New("password must be at least 12 characters"))
	}

	hasUpper := strings.ContainsFunc(password, unicode.IsUpper)
	hasLower := strings.ContainsFunc(password, unicode.IsLower)
	hasDigit := strings.ContainsFunc(password, unicode.IsDigit)
	hasSpecial := strings.ContainsFunc(password, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return errs.EInvalid("password", errors.New("password must contain at least one uppercase letter, lowercase letter, digit, and special character"))
	}

	return nil
}
