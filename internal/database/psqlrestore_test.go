package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRestoreTooling(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	for _, name := range []string{"psql", "docker", "podman"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatalf("write %s shim: %v", name, err)
		}
	}
	emptyDir := t.TempDir()

	cases := []struct {
		name      string
		pathDir   string
		runtime   string
		container string
		wantErr   bool
		errSubstr string
	}{
		{name: "host with psql available", pathDir: binDir, runtime: "host", container: "", wantErr: false},
		{name: "host with psql missing", pathDir: emptyDir, runtime: "host", container: "", wantErr: true, errSubstr: "psql not found"},
		{name: "empty runtime treated as host", pathDir: emptyDir, runtime: "", container: "", wantErr: true, errSubstr: "psql not found"},
		{name: "docker with binary and container", pathDir: binDir, runtime: "docker", container: "edub-postgres", wantErr: false},
		{name: "docker with binary missing", pathDir: emptyDir, runtime: "docker", container: "edub-postgres", wantErr: true, errSubstr: "not found on PATH"},
		{name: "docker missing container", pathDir: binDir, runtime: "docker", container: "", wantErr: true, errSubstr: "database.container is required"},
		{name: "podman with binary and container", pathDir: binDir, runtime: "podman", container: "edub-postgres", wantErr: false},
		{name: "podman with binary missing", pathDir: emptyDir, runtime: "podman", container: "edub-postgres", wantErr: true, errSubstr: "not found on PATH"},
		{name: "remote refuses", pathDir: binDir, runtime: "remote", container: "", wantErr: true, errSubstr: "remote is not supported"},
		{name: "invalid runtime value", pathDir: binDir, runtime: "ssh", container: "", wantErr: true, errSubstr: "database.runtime must be"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.pathDir)
			err := CheckRestoreTooling(tc.runtime, tc.container)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckRestoreTooling(%q, %q) = nil, want error", tc.runtime, tc.container)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckRestoreTooling(%q, %q) = %v, want nil", tc.runtime, tc.container, err)
			}
		})
	}
}
