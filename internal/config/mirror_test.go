package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsRemoteMirrorTarget(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"empty", "", false},
		{"absolute local", "/var/mirror", false},
		{"tilde local", "~/mirror", false},
		{"relative local", "backups/mirror", false},
		{"local with colon-no-slash", "host:path", true},
		{"user at host", "user@host:/srv/documents", true},
		{"rsync module", "rsync://host/module", true},
		{"host only colon", "host:module", true},
		{"path with slash prefix rejected", "/host:path", false},
		{"path with internal slash before colon", "a/b:path", false},
		{"whitespace host rejected", "ho st:path", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRemoteMirrorTarget(tc.path); got != tc.want {
				t.Errorf("IsRemoteMirrorTarget(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestValidateMirrorDestination_DashPrefixRefused(t *testing.T) {
	storage := t.TempDir()
	err := ValidateMirrorDestination("--dry-run", storage, "")
	if err == nil {
		t.Fatal("expected dash-prefixed destination to be refused")
	}
	if !strings.Contains(err.Error(), "starts with '-'") {
		t.Errorf("error should mention '-', got: %v", err)
	}
}

func TestValidateMirrorDestination_RemoteSkipped(t *testing.T) {
	storage := t.TempDir()
	backup := t.TempDir()
	if err := ValidateMirrorDestination("user@host:/path", storage, backup); err != nil {
		t.Errorf("remote destination should be accepted without local resolution: %v", err)
	}
}

func TestValidateMirrorDestination_RefusesInsideStorage(t *testing.T) {
	storage := t.TempDir()
	if err := ValidateMirrorDestination(filepath.Join(storage, "subdir"), storage, ""); err == nil {
		t.Error("expected dest inside storage to be refused")
	}
}

func TestValidateMirrorDestination_RefusesStorageItself(t *testing.T) {
	storage := t.TempDir()
	if err := ValidateMirrorDestination(storage, storage, ""); err == nil {
		t.Error("expected dest == storage to be refused")
	}
}

func TestValidateMirrorDestination_RefusesAncestorOfStorage(t *testing.T) {
	storage := t.TempDir()
	// An ancestor that is not the storage dir itself but contains it.
	// On the test runner, t.TempDir() may yield /tmp/TestXxxxxx.../001
	// whose parent /tmp/TestXxxxxx... exists and is not equal to storage.
	parent := filepath.Dir(storage)
	if parent == storage {
		t.Skip("cannot create an ancestor different from storage in this environment")
	}
	if err := ValidateMirrorDestination(parent, storage, ""); err == nil {
		t.Errorf("expected dest containing storage to be refused (dest=%s, storage=%s)", parent, storage)
	}
}

func TestValidateMirrorDestination_RefusesInsideBackup(t *testing.T) {
	storage := t.TempDir()
	backup := t.TempDir()
	if err := ValidateMirrorDestination(filepath.Join(backup, "archive"), storage, backup); err == nil {
		t.Error("expected dest inside backup to be refused")
	}
}

func TestValidateMirrorDestination_AcceptsUnrelatedLocal(t *testing.T) {
	storage := t.TempDir()
	backup := t.TempDir()
	dest := t.TempDir()
	if err := ValidateMirrorDestination(dest, storage, backup); err != nil {
		t.Errorf("unrelated destination should be accepted: %v", err)
	}
}

func TestValidateMirrorDestination_RefusesSymlinkIntoStorage(t *testing.T) {
	storage := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "link-into-storage")
	if err := os.Symlink(storage, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if err := ValidateMirrorDestination(link, storage, ""); err == nil {
		t.Errorf("expected symlink-into-storage to be refused (link=%s, storage=%s)", link, storage)
	}
}