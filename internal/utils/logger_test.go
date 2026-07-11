package utils

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewLogger_LevelParsing(t *testing.T) {
	tests := []struct {
		input string
		want  LogLevel
	}{
		{"debug", LevelDebug},
		{"info", LevelInfo},
		{"error", LevelError},
		{"fatal", LevelFatal},
		{"silent", LevelSilent},
		{"DEBUG", LevelDebug},
		{"Info", LevelInfo},
		{"unknown", LevelInfo},
		{"", LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			l := NewLogger(tt.input)
			if l.Level() != tt.want {
				t.Errorf("NewLogger(%q).Level() = %d, want %d", tt.input, l.Level(), tt.want)
			}
		})
	}
}

func TestSetLevel_DynamicChange(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelError)

	l.Debug(nil, "should not appear")
	l.Info(nil, "should not appear")
	l.Error(nil, "visible")

	got := buf.String()
	if strings.Contains(got, "should not appear") {
		t.Errorf("expected debug/info suppressed at error level, got:\n%s", got)
	}
	if !strings.Contains(got, "visible") {
		t.Errorf("expected error message at error level, got:\n%s", got)
	}
}

func TestInfo_LevelGating(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantMsg bool
	}{
		{"info at debug", "debug", true},
		{"info at info", "info", true},
		{"info at error", "error", false},
		{"info at fatal", "fatal", false},
		{"info at silent", "silent", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := NewLoggerWithWriter(&buf)
			l.SetLevel(parseLogLevel(tt.level))

			l.Info(nil, "hello info")
			got := strings.Contains(buf.String(), "hello info")
			if got != tt.wantMsg {
				t.Errorf("Info visible=%v, want %v (buffer: %q)", got, tt.wantMsg, buf.String())
			}
		})
	}
}

func TestError_LevelGating(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantMsg bool
	}{
		{"error at debug", "debug", true},
		{"error at info", "info", true},
		{"error at error", "error", true},
		{"error at fatal", "fatal", false},
		{"error at silent", "silent", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := NewLoggerWithWriter(&buf)
			l.SetLevel(parseLogLevel(tt.level))

			l.Error(nil, "hello error")
			got := strings.Contains(buf.String(), "hello error")
			if got != tt.wantMsg {
				t.Errorf("Error visible=%v, want %v (buffer: %q)", got, tt.wantMsg, buf.String())
			}
		})
	}
}

func TestDebug_LevelGating(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantMsg bool
	}{
		{"debug at debug", "debug", true},
		{"debug at info", "info", false},
		{"debug at error", "error", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := NewLoggerWithWriter(&buf)
			l.SetLevel(parseLogLevel(tt.level))

			l.Debug(nil, "hello debug")
			got := strings.Contains(buf.String(), "hello debug")
			if got != tt.wantMsg {
				t.Errorf("Debug visible=%v, want %v (buffer: %q)", got, tt.wantMsg, buf.String())
			}
		})
	}
}

func TestReqIDPrefix(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	reqID := "abc-123"
	l.Info(&reqID, "request handled")

	got := buf.String()
	if !strings.Contains(got, "REQID=abc-123") {
		t.Errorf("expected REQID prefix in output, got:\n%s", got)
	}
}

func TestReqIDPrefix_Nil(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	l.Info(nil, "no reqid here")

	got := buf.String()
	if strings.Contains(got, "REQID=") {
		t.Errorf("unexpected REQID prefix in output, got:\n%s", got)
	}
	if !strings.Contains(got, "no reqid here") {
		t.Errorf("expected message content, got:\n%s", got)
	}
}

func TestFormatStringInterpolation(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	l.Info(nil, "count=%d name=%s", 42, "test")

	got := buf.String()
	if !strings.Contains(got, "count=42 name=test") {
		t.Errorf("expected formatted message, got:\n%s", got)
	}
}

func TestConsoleFormat_PriPrefix(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	l.Info(nil, "msg")
	l.Error(nil, "msg")
	l.Debug(nil, "msg")

	got := buf.String()
	lines := strings.SplitSeq(strings.TrimSpace(got), "\n")
	for line := range lines {
		if !regexp.MustCompile(`^<\d>`).MatchString(line) {
			t.Errorf("line missing syslog prefix: %q", line)
		}
	}
}

func TestConsoleFormat_SourceLocation(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	l.Info(nil, "test")

	got := buf.String()
	if !strings.Contains(got, "logger_test.go:") {
		t.Errorf("expected caller source file in output, got:\n%s", got)
	}
}

func TestConsoleFormat_TimestampFormat(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	l.Info(nil, "test")

	got := buf.String()
	// YYYY/MM/DD HH:MM:SS pattern
	if !regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}`).MatchString(got) {
		t.Errorf("expected timestamp in output, got:\n%s", got)
	}
}

func TestNewDiscardLogger_NoOutput(t *testing.T) {
	l := NewDiscardLogger()

	// Force any output — discard logger suppresses at the handler level.
	l.Info(nil, "should vanish")
	l.Error(nil, "should vanish")
	l.Debug(nil, "should vanish")

	// Verify it doesn't panic and Level returns the expected default.
	if l.Level() != LevelInfo {
		t.Errorf("NewDiscardLogger().Level() = %d, want LevelInfo (%d)", l.Level(), LevelInfo)
	}
}

func TestNewDiscardLogger_SetLevel(t *testing.T) {
	l := NewDiscardLogger()
	l.SetLevel(LevelDebug)
	if l.Level() != LevelDebug {
		t.Errorf("SetLevel on discard logger: got %d, want %d", l.Level(), LevelDebug)
	}
}

func TestSetLogFile_AppendMode(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	l := NewLogger("info")
	if err := l.SetLogFile(LogFileConfig{Path: logPath}); err != nil {
		t.Fatalf("SetLogFile: %v", err)
	}

	l.Info(nil, "first line")
	l.Info(nil, "second line")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "first line") {
		t.Errorf("expected 'first line' in log file, got:\n%s", content)
	}
	if !strings.Contains(content, "second line") {
		t.Errorf("expected 'second line' in log file, got:\n%s", content)
	}
}

func TestSetLogFile_InvalidPath(t *testing.T) {
	l := NewLogger("info")
	err := l.SetLogFile(LogFileConfig{Path: "/nonexistent/dir/test.log"})
	if err == nil {
		t.Error("expected error for invalid path, got nil")
	}
}

func TestFileHandler_CapturesAllLevels(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "alllevels.log")

	var consoleBuf bytes.Buffer
	l := NewLoggerWithWriter(&consoleBuf)
	l.SetLevel(LevelError) // suppress info/debug on console
	if err := l.SetLogFile(LogFileConfig{Path: logPath}); err != nil {
		t.Fatalf("SetLogFile: %v", err)
	}

	l.Info(nil, "info msg")
	l.Debug(nil, "debug msg")
	l.Error(nil, "error msg")

	// Console should only have error.
	if strings.Contains(consoleBuf.String(), "info msg") {
		t.Error("info should be suppressed on console at error level")
	}
	if strings.Contains(consoleBuf.String(), "debug msg") {
		t.Error("debug should be suppressed on console at error level")
	}

	// File should have all three.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	for _, want := range []string{"info msg", "debug msg", "error msg"} {
		if !strings.Contains(content, want) {
			t.Errorf("file missing %q, got:\n%s", want, content)
		}
	}
}

func TestFileFormat_NoSourceLocation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "fileformat.log")

	var consoleBuf bytes.Buffer
	l := NewLoggerWithWriter(&consoleBuf)
	if err := l.SetLogFile(LogFileConfig{Path: logPath}); err != nil {
		t.Fatalf("SetLogFile: %v", err)
	}

	l.Info(nil, "test file msg")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)

	// File format should NOT contain source file:line (only console does).
	if strings.Contains(content, "logger_test.go:") {
		t.Errorf("file output should not contain source location, got:\n%s", content)
	}
	if !strings.Contains(content, "test file msg") {
		t.Errorf("file output missing message, got:\n%s", content)
	}
}

func TestFileFormat_LevelPrefix(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "levelfmt.log")

	var consoleBuf bytes.Buffer
	l := NewLoggerWithWriter(&consoleBuf)
	if err := l.SetLogFile(LogFileConfig{Path: logPath}); err != nil {
		t.Fatalf("SetLogFile: %v", err)
	}

	l.Info(nil, "info-line")
	l.Error(nil, "error-line")
	l.Debug(nil, "debug-line")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)

	// File format: "YYYY/MM/DD HH:MM:SS LEVEL : msg" — no syslog prefix.
	if !strings.Contains(content, "INFO  : info-line") {
		t.Errorf("expected INFO level prefix in file, got:\n%s", content)
	}
	if !strings.Contains(content, "ERROR : error-line") {
		t.Errorf("expected ERROR level prefix in file, got:\n%s", content)
	}
	if !strings.Contains(content, "DEBUG : debug-line") {
		t.Errorf("expected DEBUG level prefix in file, got:\n%s", content)
	}
	// No syslog pri prefix in file.
	if strings.Contains(content, "<6>") || strings.Contains(content, "<3>") {
		t.Errorf("file output should not contain syslog pri prefix, got:\n%s", content)
	}
}

func TestSlogLogger_BridgesToHandler(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithWriter(&buf)
	l.SetLevel(LevelDebug)

	slog := l.SlogLogger()
	slog.Info("slog message")

	got := buf.String()
	if !strings.Contains(got, "slog message") {
		t.Errorf("SlogLogger output not routed to writer, got:\n%s", got)
	}
}

func TestLevelPriority(t *testing.T) {
	tests := []struct {
		slogLevel slog.Level
		wantPri   int
	}{
		{slog.LevelDebug, 7},
		{slog.LevelInfo, 6},
		{slog.LevelError, 3},
		{levelFatalSlog, 2},
		{slog.Level(13), 2},
	}
	for _, tt := range tests {
		got := levelPriority(tt.slogLevel)
		if got != tt.wantPri {
			t.Errorf("levelPriority(%d) = %d, want %d", tt.slogLevel, got, tt.wantPri)
		}
	}
}

func TestLevelName(t *testing.T) {
	tests := []struct {
		slogLevel slog.Level
		want      string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelError, "ERROR"},
		{levelFatalSlog, "FATAL"},
	}
	for _, tt := range tests {
		got := levelName(tt.slogLevel)
		if got != tt.want {
			t.Errorf("levelName(%d) = %q, want %q", tt.slogLevel, got, tt.want)
		}
	}
}
