//go:build cgo

package tagmatcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySHA256_Match(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	// sha256("hello world")
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := verifySHA256(path, want); err != nil {
		t.Errorf("verifySHA256: unexpected error for matching content: %v", err)
	}
}

func TestVerifySHA256_Mismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("tampered content"), 0644); err != nil {
		t.Fatal(err)
	}
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := verifySHA256(path, want); err == nil {
		t.Error("verifySHA256: expected error for mismatched content, got nil")
	}
}
