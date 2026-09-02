package config

import (
	"strings"
	"testing"
)

func TestValidateDatabaseRuntime(t *testing.T) {
	cases := []struct {
		name      string
		runtime   string
		container string
		wantErr   bool
		errSubstr string
	}{
		{name: "empty runtime treated as host", runtime: "", container: "", wantErr: false},
		{name: "host runtime ignores container", runtime: "host", container: "", wantErr: false},
		{name: "docker with valid container", runtime: "docker", container: "edub-postgres", wantErr: false},
		{name: "podman with valid container", runtime: "podman", container: "edub_postgres-1", wantErr: false},
		{name: "remote does not require container", runtime: "remote", container: "", wantErr: false},
		{name: "invalid runtime value", runtime: "ssh", container: "", wantErr: true, errSubstr: "database.runtime"},
		{name: "docker missing container", runtime: "docker", container: "", wantErr: true, errSubstr: "database.container is required"},
		{name: "podman missing container", runtime: "podman", container: "", wantErr: true, errSubstr: "database.container is required"},
		{name: "docker with single-char container", runtime: "docker", container: "a", wantErr: true, errSubstr: "invalid characters"},
		{name: "docker with slash injection", runtime: "docker", container: "evil/name", wantErr: true, errSubstr: "invalid characters"},
		{name: "docker with shell metachar", runtime: "docker", container: "a;b", wantErr: true, errSubstr: "invalid characters"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDatabaseRuntime(tc.runtime, tc.container)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateDatabaseRuntime(%q, %q) = nil, want error", tc.runtime, tc.container)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateDatabaseRuntime(%q, %q) = %v, want nil", tc.runtime, tc.container, err)
			}
		})
	}
}
