package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyTree_CopiesNestedTree(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a", "b", "leaf.txt"), []byte("leaf"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CopyTree(context.Background(), src, dst); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	root, err := os.ReadFile(filepath.Join(dst, "root.txt"))
	if err != nil {
		t.Fatalf("read copied root file: %v", err)
	}
	if string(root) != "root" {
		t.Errorf("root.txt = %q, want %q", root, "root")
	}
	leaf, err := os.ReadFile(filepath.Join(dst, "a", "b", "leaf.txt"))
	if err != nil {
		t.Fatalf("read copied nested file: %v", err)
	}
	if string(leaf) != "leaf" {
		t.Errorf("leaf.txt = %q, want %q", leaf, "leaf")
	}
	if _, err := os.Stat(filepath.Join(src, "root.txt")); err != nil {
		t.Errorf("source must survive a copy: %v", err)
	}
}

func TestCopyTree_CancelledContextCreatesNoDestination(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := CopyTree(ctx, src, dst)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CopyTree error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("destination must not exist after a cancelled copy, stat = %v", err)
	}
}
