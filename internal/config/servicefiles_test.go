package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateServiceFiles(t *testing.T) {
	configDir := t.TempDir()
	fakeBinDir := t.TempDir()
	fakeKushim := filepath.Join(fakeBinDir, "kushim")
	if err := os.WriteFile(fakeKushim, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	svcDir, err := GenerateServiceFiles(configDir)
	if err != nil {
		t.Fatalf("GenerateServiceFiles: %v", err)
	}

	if svcDir != filepath.Join(configDir, "systemd") {
		t.Fatalf("svcDir = %q, want %s/systemd", svcDir, configDir)
	}

	wantFiles := []string{
		"kushim-hugot.service",
		"kushim-queue.service",
		"edub.service",
		"edub-kushim.target",
	}

	for _, name := range wantFiles {
		path := filepath.Join(svcDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		content := string(data)

		if !strings.Contains(content, "Documentation=https://github.com/wgomg/edub-kushim") {
			t.Errorf("%s: missing Documentation= line", name)
		}
	}

	for _, name := range []string{"kushim-hugot.service", "kushim-queue.service", "edub.service"} {
		data, _ := os.ReadFile(filepath.Join(svcDir, name))
		if !strings.Contains(string(data), "PartOf=edub-kushim.target") {
			t.Errorf("%s: missing PartOf=edub-kushim.target", name)
		}
	}

	hugot, _ := os.ReadFile(filepath.Join(svcDir, "kushim-hugot.service"))
	if !strings.Contains(string(hugot), "Type=notify") {
		t.Error("kushim-hugot.service: expected Type=notify")
	}
	if !strings.Contains(string(hugot), "NotifyAccess=main") {
		t.Error("kushim-hugot.service: expected NotifyAccess=main")
	}
	if !strings.Contains(string(hugot), "TimeoutStartSec=120") {
		t.Error("kushim-hugot.service: expected TimeoutStartSec=120")
	}
	if !strings.Contains(string(hugot), "ExecStart="+fakeKushim+" hugot --socket") {
		t.Errorf("kushim-hugot.service: ExecStart should use resolved kushim path, got:\n%s", hugot)
	}

	queue, _ := os.ReadFile(filepath.Join(svcDir, "kushim-queue.service"))
	if !strings.Contains(string(queue), "After=network.target kushim-hugot.service") {
		t.Error("kushim-queue.service: expected After=kushim-hugot.service")
	}
	if !strings.Contains(string(queue), "Wants=kushim-hugot.service") {
		t.Error("kushim-queue.service: expected Wants=kushim-hugot.service")
	}
	if !strings.Contains(string(queue), "ExecStart="+fakeKushim+" queue") {
		t.Errorf("kushim-queue.service: ExecStart should use resolved kushim path, got:\n%s", queue)
	}

	edub, _ := os.ReadFile(filepath.Join(svcDir, "edub.service"))
	edubBin := filepath.Join(fakeBinDir, "edub")
	if !strings.Contains(string(edub), "ExecStart="+edubBin) {
		t.Errorf("edub.service: ExecStart should be sibling edub binary, got:\n%s", edub)
	}

	target, _ := os.ReadFile(filepath.Join(svcDir, "edub-kushim.target"))
	if !strings.Contains(string(target), "Wants=kushim-hugot.service kushim-queue.service edub.service") {
		t.Error("edub-kushim.target: missing Wants= for all three services")
	}
}

func TestGenerateServiceFiles_Idempotent(t *testing.T) {
	configDir := t.TempDir()
	fakeBinDir := t.TempDir()
	fakeKushim := filepath.Join(fakeBinDir, "kushim")
	if err := os.WriteFile(fakeKushim, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	svcDir1, err := GenerateServiceFiles(configDir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	svcDir2, err := GenerateServiceFiles(configDir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if svcDir1 != svcDir2 {
		t.Fatalf("svcDir mismatch: %q vs %q", svcDir1, svcDir2)
	}

	hugotPath := filepath.Join(svcDir1, "kushim-hugot.service")
	data, err := os.ReadFile(hugotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Type=notify") {
		t.Error("file content corrupted after second call")
	}
}

func TestGenerateServiceFiles_KushimNotFound(t *testing.T) {
	configDir := t.TempDir()
	emptyBinDir := t.TempDir()
	t.Setenv("PATH", emptyBinDir)

	_, err := GenerateServiceFiles(configDir)
	if err == nil {
		t.Fatal("expected error when kushim is not on PATH")
	}
	if !strings.Contains(err.Error(), "kushim") {
		t.Errorf("error should mention kushim: %v", err)
	}
}

func TestSanitizeUnitField(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"clean string", "hello", false},
		{"empty string", "", false},
		{"with slash", "/home/user", false},
		{"newline rejected", "hello\nworld", true},
		{"carriage return rejected", "hello\rworld", true},
		{"both rejected", "a\n\rb", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sanitizeUnitField(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeUnitField(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
