package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestParseValue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want any
	}{
		{"bool true", "true", true},
		{"bool false", "false", false},
		{"bool case insensitive", "TRUE", true},
		{"int", "3000", 3000},
		{"negative int", "-1", -1},
		{"int zero", "0", 0},
		{"float", "3.14", 3.14},
		{"list comma separated", "eng,spa,deu", []string{"eng", "spa", "deu"}},
		{"list with spaces", "eng, spa, deu", []string{"eng", "spa", "deu"}},
		{"single comma", "a,b", []string{"a", "b"}},
		{"string", "hello", "hello"},
		{"string empty", "", ""},
		{"string with spaces", "hello world", "hello world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseValue(tt.raw)
			switch want := tt.want.(type) {
			case bool:
				if got != want {
					t.Errorf("parseValue(%q) = %v (%T), want %v (%T)", tt.raw, got, got, want, want)
				}
			case int:
				if got != want {
					t.Errorf("parseValue(%q) = %v (%T), want %v (%T)", tt.raw, got, got, want, want)
				}
			case float64:
				if got != want {
					t.Errorf("parseValue(%q) = %v (%T), want %v (%T)", tt.raw, got, got, want, want)
				}
			case string:
				if got != want {
					t.Errorf("parseValue(%q) = %v (%T), want %v (%T)", tt.raw, got, got, want, want)
				}
			case []string:
				gotSlice, ok := got.([]string)
				if !ok {
					t.Fatalf("parseValue(%q) = %v (%T), want []string", tt.raw, got, got)
				}
				if len(gotSlice) != len(want) {
					t.Fatalf("parseValue(%q) = %v, want %v", tt.raw, gotSlice, want)
				}
				for i := range gotSlice {
					if gotSlice[i] != want[i] {
						t.Errorf("parseValue(%q)[%d] = %q, want %q", tt.raw, i, gotSlice[i], want[i])
					}
				}
			default:
				t.Fatalf("unhandled want type %T", tt.want)
			}
		})
	}
}

func TestDeleteNestedKey(t *testing.T) {
	tests := []struct {
		name    string
		m       map[string]any
		key     string
		want    bool
		remains map[string]any
	}{
		{
			name: "simple key exists",
			m:    map[string]any{"a": 1},
			key:  "a",
			want: true,
			remains: map[string]any{},
		},
		{
			name: "simple key missing",
			m:    map[string]any{"a": 1},
			key:  "b",
			want: false,
			remains: map[string]any{"a": 1},
		},
		{
			name: "nested key exists",
			m: map[string]any{
				"a": map[string]any{"b": 2},
			},
			key:     "a.b",
			want:    true,
			remains: map[string]any{"a": map[string]any{}},
		},
		{
			name: "nested key missing leaf",
			m: map[string]any{
				"a": map[string]any{"b": 2},
			},
			key: "a.c",
			want: false,
			remains: map[string]any{
				"a": map[string]any{"b": 2},
			},
		},
		{
			name: "deeply nested key",
			m: map[string]any{
				"a": map[string]any{
					"b": map[string]any{"c": 3},
				},
			},
			key:  "a.b.c",
			want: true,
			remains: map[string]any{
				"a": map[string]any{
					"b": map[string]any{},
				},
			},
		},
		{
			name: "intermediate not a map",
			m: map[string]any{
				"a": "not-a-map",
			},
			key:     "a.b",
			want:    false,
			remains: map[string]any{"a": "not-a-map"},
		},
		{
			name: "empty map",
			m:    map[string]any{},
			key:  "a",
			want: false,
			remains: map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deleteNestedKey(tt.m, tt.key)
			if got != tt.want {
				t.Errorf("deleteNestedKey() = %v, want %v", got, tt.want)
			}
			if len(tt.m) != len(tt.remains) {
				t.Errorf("map length = %d, want %d", len(tt.m), len(tt.remains))
			}
			for k, v := range tt.remains {
				if tt.m[k] == nil {
					t.Errorf("key %q missing from map after delete", k)
				} else if vv, ok := tt.m[k].(map[string]any); ok {
					if rvv, ok := v.(map[string]any); ok {
						if len(vv) != len(rvv) {
							t.Errorf("nested map for key %q: got %d entries, want %d", k, len(vv), len(rvv))
						}
					}
				}
			}
		})
	}
}

func newTestContainer(t *testing.T) (*Container, string) {
	t.Helper()
	configDir := t.TempDir()
	writeMinimalTestConfig(t, configDir)
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	logger := utils.NewLogger("error")
	c := NewContainer(cfg, logger)
	t.Cleanup(func() { c.Close() })
	return c, configDir
}

func writeMinimalTestConfig(t *testing.T, configDir string) {
	t.Helper()
	yaml := `consumer:
  ocr:
    languages:
      - eng
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigHandler_Help(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"--help"})
	if err != nil {
		t.Errorf("configHandler --help returned error: %v", err)
	}
}

func TestConfigHandler_Path(t *testing.T) {
	c, configDir := newTestContainer(t)
	err := configHandler(c, []string{"--path"})
	if err != nil {
		t.Fatalf("configHandler --path: %v", err)
	}
	want := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected config path %s does not exist", want)
	}
}

func TestConfigHandler_Validate(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"--validate"})
	if err != nil {
		t.Fatalf("configHandler --validate: %v", err)
	}
}

func TestConfigHandler_ValidateInvalid(t *testing.T) {
	c, configDir := newTestContainer(t)
	badYAML := `consumer:
  ocr:
    languages: []
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	err := configHandler(c, []string{"--validate"})
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

func TestConfigHandler_Get(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"consumer.ocr.languages"})
	if err != nil {
		t.Fatalf("configHandler get: %v", err)
	}
}

func TestConfigHandler_GetMissingKey(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"nonexistent.key"})
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

func TestConfigHandler_Set(t *testing.T) {
	c, configDir := newTestContainer(t)
	err := configHandler(c, []string{"server.port", "9999"})
	if err != nil {
		t.Fatalf("configHandler set: %v", err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load after set: %v", err)
	}
	if cfg.Srv.Port != 9999 {
		t.Errorf("server.port = %d, want 9999", cfg.Srv.Port)
	}
}

func TestConfigHandler_SetInvalid(t *testing.T) {
	c, configDir := newTestContainer(t)
	origData, _ := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	err := configHandler(c, []string{"server.port", "not_a_number"})
	if err == nil {
		t.Error("expected error for invalid value, got nil")
	}
	newData, _ := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if string(origData) != string(newData) {
		t.Error("config file was modified despite invalid set")
	}
}

func TestConfigHandler_Unset(t *testing.T) {
	c, configDir := newTestContainer(t)
	err := configHandler(c, []string{"--unset", "server.port"})
	if err != nil {
		t.Fatalf("configHandler --unset: %v", err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load after unset: %v", err)
	}
	if cfg.Srv.Port != 3000 {
		t.Errorf("server.port = %d, want 3000 (default after unset)", cfg.Srv.Port)
	}
}

func TestConfigHandler_UnsetMissingKey(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"--unset", "nonexistent.key"})
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

func TestConfigHandler_UnsetWithoutKey(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"--unset"})
	if err == nil {
		t.Error("expected error for --unset without key, got nil")
	}
}

func TestConfigHandler_DumpAll(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{})
	if err != nil {
		t.Fatalf("configHandler dump: %v", err)
	}
}

func TestConfigHandler_UnknownArgs(t *testing.T) {
	c, _ := newTestContainer(t)
	err := configHandler(c, []string{"--validate", "extra"})
	if err == nil {
		t.Error("expected error for unknown args, got nil")
	}
}
