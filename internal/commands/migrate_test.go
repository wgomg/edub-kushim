package commands

import "testing"

func TestRejectUnsafePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty is allowed", "", false},
		{"regular path", "/var/lib/edub", false},
		{"home path", "/home/user/data", false},
		{"tmp path", "/tmp/edub", false},
		{"proc exact", "/proc", true},
		{"proc subdir", "/proc/foo", true},
		{"sys", "/sys/kernel", true},
		{"dev", "/dev/null", true},
		{"boot", "/boot/grub", true},
		{"proc-like but not", "/procmail", false},
		{"dev-like but not", "/devops", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectUnsafePath(tt.path, "--storage-dir")
			if (err != nil) != tt.wantErr {
				t.Errorf("rejectUnsafePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestMigrateStorageHandler_NoOp(t *testing.T) {
	c, _ := newTestContainer(t)
	cfg := c.cfg.Load()
	err := migrateStorageHandler(c, []string{
		"--storage-dir", cfg.Storage.StorageDir,
		"--consumption-dir", cfg.Storage.ConsumptionDir,
	})
	if err != nil {
		t.Errorf("storage no-op with unchanged dirs: %v", err)
	}
}

func TestMigrateStorageHandler_RejectsUnsafePath(t *testing.T) {
	c, _ := newTestContainer(t)
	err := migrateStorageHandler(c, []string{"--storage-dir", "/proc/foo"})
	if err == nil {
		t.Error("expected error for /proc path, got nil")
	}
}
