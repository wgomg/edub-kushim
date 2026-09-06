package utils

import "testing"

// SHA256Hex hashes the UTF-8 bytes of its input — that is what
// `[]byte(s)` produces in Go, and it matches the content-identity contract
// (R2 §2: input_hash over the bytes fed to Reduce, which is the stored
// extracted text).
// Known-vector digests below are lowercase hex and were verified against
// the `sha256sum` CLI.
func TestSHA256Hex_KnownVectors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"hello world", "hello world", "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"},
		{"unicode", "café", "850f7dc43910ff890f8879c0ed26fe697c93a067ad93a7d50f466a7028a9bf4e"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SHA256Hex(c.in)
			if got != c.want {
				t.Fatalf("SHA256Hex(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSHA256Hex_DifferentInputsDifferentOutputs(t *testing.T) {
	a := SHA256Hex("alpha")
	b := SHA256Hex("beta")
	if a == b {
		t.Fatalf("expected different digests for distinct inputs, both = %q", a)
	}
}
