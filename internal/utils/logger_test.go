package utils

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  LogLevel
	}{
		{"silent", LevelSilent},
		{"fatal", LevelFatal},
		{"error", LevelError},
		{"info", LevelInfo},
		{"debug", LevelDebug},
		{"SILENT", LevelSilent},
		{"INFO", LevelInfo},
		{"unknown", LevelInfo},
		{"", LevelInfo},
	}
	for _, tc := range tests {
		got := parseLogLevel(tc.input)
		if got != tc.want {
			t.Errorf("parseLogLevel(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestNewDiscardLogger(t *testing.T) {
	l := NewDiscardLogger()
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewLogger(t *testing.T) {
	l := NewLogger("info")
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
	if l.level != LevelInfo {
		t.Errorf("level = %d", l.level)
	}
}

func TestNewLoggerWithWriter(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
	l.Info(nil, "hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected 'hello' in output, got %q", buf.String())
	}
}

func TestSetLevel(t *testing.T) {
	l := NewDiscardLogger()
	l.SetLevel(LevelDebug)
	if l.Level() != LevelDebug {
		t.Errorf("level = %d, want %d", l.Level(), LevelDebug)
	}
}

func TestLevel(t *testing.T) {
	l := NewDiscardLogger()
	if l.Level() != LevelInfo {
		t.Errorf("default level = %d", l.Level())
	}
}

func TestInfo_LevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelError)

	l.Info(nil, "should not appear")
	if buf.Len() > 0 {
		t.Errorf("Info wrote when level is Error: %q", buf.String())
	}
}

func TestInfo_WithReqID(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	reqID := "abc123"
	l.Info(&reqID, "test message")
	out := buf.String()
	if !strings.Contains(out, "REQID=abc123") {
		t.Errorf("expected REQID in output, got %q", out)
	}
	if !strings.Contains(out, "test message") {
		t.Errorf("expected message in output, got %q", out)
	}
}

func TestInfo_Format(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.Info(nil, "val=%d", 42)
	out := buf.String()
	if !strings.Contains(out, "val=42") {
		t.Errorf("expected 'val=42' in output, got %q", out)
	}
}

func TestError_LevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelFatal)

	l.Error(nil, "should not appear")
	if buf.Len() > 0 {
		t.Errorf("Error wrote when level is Fatal: %q", buf.String())
	}
}

func TestError_WithReqID(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	reqID := "err-1"
	l.Error(&reqID, "something went wrong")
	out := buf.String()
	if !strings.Contains(out, "REQID=err-1") {
		t.Errorf("expected REQID in output, got %q", out)
	}
}

func TestDebug_LevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelInfo)

	l.Debug(nil, "should not appear")
	if buf.Len() > 0 {
		t.Errorf("Debug wrote when level is Info: %q", buf.String())
	}
}

func TestDebug_WithReqID(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)
	reqID := "dbg-1"
	l.Debug(&reqID, "debug info")
	out := buf.String()
	if !strings.Contains(out, "REQID=dbg-1") {
		t.Errorf("expected REQID in output, got %q", out)
	}
	if !strings.Contains(out, "debug info") {
		t.Errorf("expected message in output, got %q", out)
	}
}

func TestInfo_ReqIDSpaces(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	reqID := "space id"
	l.Info(&reqID, "msg")
	out := buf.String()
	if !strings.Contains(out, "REQID=space id") {
		t.Errorf("expected REQID with spaces in output, got %q", out)
	}
}

func TestNewLogger_Stdout(t *testing.T) {
	l := NewLogger("info")
	if l.infoLogger.Writer() != os.Stdout {
		t.Errorf("infoLogger should write to stdout")
	}
	if l.errorLogger.Writer() != os.Stderr {
		t.Errorf("errorLogger should write to stderr")
	}
}
