package commands

import (
	"testing"
)

func TestNewCommandRunner(t *testing.T) {
	r := NewCommandRunner(nil)
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestExecuteCommand_Unknown(t *testing.T) {
	r := NewCommandRunner(nil)
	err := r.ExecuteCommand("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecuteCommand_Version(t *testing.T) {
	r := NewCommandRunner(nil)
	err := r.ExecuteCommand("version", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListCommands(t *testing.T) {
	cmds := ListCommands()
	names := make(map[string]bool)
	for _, c := range cmds {
		names[c.Name] = true
	}
	for _, want := range []string{"version", "consume", "search", "task", "setup"} {
		if !names[want] {
			t.Errorf("missing command: %s", want)
		}
	}
}
