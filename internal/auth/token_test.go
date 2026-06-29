package auth

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateSessionSecret(t *testing.T) {
	s, err := GenerateSessionSecret()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s) != 64 {
		t.Fatalf("expected 64-char hex string, got %d chars: %s", len(s), s)
	}
	if _, err := hex.DecodeString(s); err != nil {
		t.Fatalf("expected valid hex, got %s: %v", s, err)
	}
}

func TestGenerateToken_ValidateRoundTrip(t *testing.T) {
	secret := "test-secret-key"
	claims, err := GenerateToken(42, "alice", secret)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if claims == "" {
		t.Fatal("expected non-empty token string")
	}

	got, err := ValidateToken(claims, secret)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if got.UserID != 42 {
		t.Errorf("UserID: got %d, want 42", got.UserID)
	}
	if got.Username != "alice" {
		t.Errorf("Username: got %q, want %q", got.Username, "alice")
	}
	if got.IssuedAt == nil {
		t.Error("expected iat to be set")
	}
	if got.ExpiresAt == nil {
		t.Error("expected exp to be set")
	}
	if got.ExpiresAt.Time.Before(time.Now()) {
		t.Error("expected exp to be in the future")
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	token, _ := GenerateToken(1, "user", "correct-secret")
	_, err := ValidateToken(token, "wrong-secret")
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestValidateToken_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	claims := Claims{
		UserID:   1,
		Username: "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	_, err = ValidateToken(tokenString, secret)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestValidateToken_Malformed(t *testing.T) {
	_, err := ValidateToken("not-a-jwt", "secret")
	if err == nil {
		t.Fatal("expected error for malformed token")
	}
}

func TestGenerateToken_Parts(t *testing.T) {
	token, _ := GenerateToken(1, "u", "s")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
}
