// Package log provides the process logger: leveled console output, an
// optional log file that mirrors every record, and the memory and duration
// formatters that feed log lines. Every package receives a *Logger from its
// constructor; the package holds no global state.
package log

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	slog "log/slog"

	"gopkg.in/natefinch/lumberjack.v2"
)

// LogLevel is the console verbosity, ordered from silent (1) to debug (7).
// It gates console output only; a configured log file always receives every
// record.
type LogLevel int

// The console levels, from silent through debug; see LogLevel.
const (
	LevelSilent LogLevel = 1
	LevelFatal  LogLevel = 2
	LevelError  LogLevel = 3
	LevelWarn   LogLevel = 4
	LevelInfo   LogLevel = 6
	LevelDebug  LogLevel = 7
)

const levelFatalSlog = slog.Level(12)

func levelName(l slog.Level) string {
	switch l {
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	case levelFatalSlog:
		return "FATAL"
	default:
		return l.String()
	}
}

func levelPriority(l slog.Level) int {
	switch {
	case l <= slog.LevelDebug:
		return 7
	case l <= slog.LevelInfo:
		return 6
	case l <= slog.LevelWarn:
		return 4
	case l <= slog.LevelError:
		return 3
	default:
		return 2
	}
}

type consoleHandler struct {
	level  *slog.LevelVar
	writer io.Writer
}

func (h *consoleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *consoleHandler) Handle(ctx context.Context, record slog.Record) error {
	pri := levelPriority(record.Level)
	name := levelName(record.Level)
	var source string
	if record.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{record.PC})
		frame, _ := frames.Next()
		source = fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)
	}
	line := fmt.Sprintf("<%d>%-6s: %s %s: %s\n", pri, name,
		record.Time.Format("2006/01/02 15:04:05"), source, record.Message)

	w := h.writer
	if w == nil {
		if record.Level >= slog.LevelWarn {
			w = os.Stderr
		} else {
			w = os.Stdout
		}
	}
	_, err := w.Write([]byte(line))
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }

func (h *consoleHandler) WithGroup(name string) slog.Handler { return h }

type fileHandler struct {
	writer io.Writer
	mu     sync.Mutex
}

func (h *fileHandler) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (h *fileHandler) Handle(ctx context.Context, record slog.Record) error {
	name := levelName(record.Level)
	line := fmt.Sprintf("%s %-6s: %s\n",
		record.Time.Format("2006/01/02 15:04:05"), name, record.Message)

	h.mu.Lock()
	_, err := h.writer.Write([]byte(line))
	h.mu.Unlock()
	return err
}

func (h *fileHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }

func (h *fileHandler) WithGroup(name string) slog.Handler { return h }

// Logger is the process-wide logger handed down through constructors. Info,
// Error, Debug and Warn write one printf-formatted line: the console copy is
// gated by the console level and carries the caller's file and line, while a
// file attached with SetLogFile receives every record without source
// locations. A non-empty reqID is prepended to the message as "REQID=<id>";
// an empty reqID logs without the prefix, and the prefix is the only
// correlation mechanism the record carries. Sink write errors are
// intentionally discarded and never propagate: a logging failure must not
// interrupt the caller, and there is no second channel to report it. All
// methods are safe for concurrent use.
type Logger struct {
	level          LogLevel
	consoleLevel   *slog.LevelVar
	consoleHandler slog.Handler
	fileHandler    slog.Handler
	fileCloser     io.Closer
	RawBodyLog     bool
}

// NewLogger returns a logger writing to the console at the given level. The
// level is matched case-insensitively and "warning" is accepted for "warn";
// anything unrecognized falls back to info, so construction never fails.
func NewLogger(level string) *Logger {
	logLevel := parseLogLevel(level)
	consoleLevel := new(slog.LevelVar)
	consoleLevel.Set(mapLogLevelToSlog(logLevel))

	return &Logger{
		level:          logLevel,
		consoleLevel:   consoleLevel,
		consoleHandler: &consoleHandler{level: consoleLevel},
	}
}

// NewDiscardLogger returns a logger that discards every record. Use it where
// a *Logger is required but output would be noise, such as in tests.
func NewDiscardLogger() *Logger {
	consoleLevel := new(slog.LevelVar)
	consoleLevel.Set(slog.Level(13))
	return &Logger{
		level:          LevelInfo,
		consoleLevel:   consoleLevel,
		consoleHandler: slog.DiscardHandler,
	}
}

// NewLoggerWithWriter returns a logger at debug level that sends its console
// output to w instead of the process stdout and stderr.
func NewLoggerWithWriter(w io.Writer) *Logger {
	consoleLevel := new(slog.LevelVar)
	consoleLevel.Set(slog.LevelDebug)
	return &Logger{
		level:        LevelInfo,
		consoleLevel: consoleLevel,
		consoleHandler: &consoleHandler{
			level:  consoleLevel,
			writer: w,
		},
	}
}

// LogFileConfig describes the log file. A MaxSize greater than zero enables
// size-based rotation with the given backup count, age and compression; zero
// opens the file in plain append mode instead.
type LogFileConfig struct {
	Path       string
	MaxSize    int
	MaxBackups int
	MaxAge     int
	Compress   bool
}

// SetLogFile attaches a log file that mirrors every record regardless of the
// console level. A later call replaces the file and closes the previous one.
// It returns an error when the plain file cannot be opened, leaving the
// previous attachment in place.
func (l *Logger) SetLogFile(cfg LogFileConfig) error {
	var writer io.Writer
	var closer io.Closer
	if cfg.MaxSize > 0 {
		lj := &lumberjack.Logger{
			Filename:   cfg.Path,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
		writer = lj
		closer = lj
	} else {
		f, err := os.OpenFile(cfg.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open log file %s: %w", cfg.Path, err)
		}
		writer = f
		closer = f
	}

	if l.fileCloser != nil {
		_ = l.fileCloser.Close()
	}

	l.fileHandler = &fileHandler{writer: writer}
	l.fileCloser = closer
	return nil
}

func parseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case "silent":
		return LevelSilent
	case "fatal":
		return LevelFatal
	case "error":
		return LevelError
	case "warn", "warning":
		return LevelWarn
	case "info":
		return LevelInfo
	case "debug":
		return LevelDebug
	default:
		return LevelInfo
	}
}

func mapLogLevelToSlog(l LogLevel) slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelError:
		return slog.LevelError
	case LevelWarn:
		return slog.LevelWarn
	case LevelFatal:
		return levelFatalSlog
	case LevelSilent:
		return slog.Level(13)
	default:
		return slog.LevelInfo
	}
}

// SetLevel changes the console level immediately. A configured log file is
// unaffected.
func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
	l.consoleLevel.Set(mapLogLevelToSlog(level))
}

// Level reports the current console level.
func (l *Logger) Level() LogLevel {
	return l.level
}

// SlogLogger exposes the console path as a standard slog.Logger so external
// libraries log through the same level gating. It does not write to the log
// file. Attributes attached through WithAttrs are not rendered by this
// handler, which formats only the message, level, time and source.
func (l *Logger) SlogLogger() *slog.Logger {
	return slog.New(l.consoleHandler)
}

func (l *Logger) log(level slog.Level, pc uintptr, reqID string, format string, v ...any) {
	msg := format
	if reqID != "" {
		msg = fmt.Sprintf("REQID=%s ", reqID) + msg
	}
	if len(v) > 0 {
		msg = fmt.Sprintf(msg, v...)
	}

	record := slog.NewRecord(time.Now(), level, msg, pc)

	if l.fileHandler != nil {
		_ = l.fileHandler.Handle(context.Background(), record)
	}
	if l.consoleHandler.Enabled(context.Background(), level) {
		_ = l.consoleHandler.Handle(context.Background(), record)
	}
}

// Info logs format and v at info level. reqID, when non-empty, is prepended
// to the message as "REQID=<id>"; format and v follow fmt.Sprintf.
func (l *Logger) Info(reqID string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelInfo, pc, reqID, format, v...)
}

// Error logs format and v at error level, with the same reqID and printf
// handling as Info.
func (l *Logger) Error(reqID string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelError, pc, reqID, format, v...)
}

// Debug logs format and v at debug level, with the same reqID and printf
// handling as Info.
func (l *Logger) Debug(reqID string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelDebug, pc, reqID, format, v...)
}

// Warn logs format and v at warn level, with the same reqID and printf
// handling as Info.
func (l *Logger) Warn(reqID string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelWarn, pc, reqID, format, v...)
}

// Fatal logs v (joined per fmt.Sprint) at fatal level to console and file,
// then exits the process with status 1.
func (l *Logger) Fatal(v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	msg := fmt.Sprint(v...)
	record := slog.NewRecord(time.Now(), levelFatalSlog, msg, pc)
	if l.fileHandler != nil {
		_ = l.fileHandler.Handle(context.Background(), record)
	}
	if l.consoleHandler.Enabled(context.Background(), levelFatalSlog) {
		_ = l.consoleHandler.Handle(context.Background(), record)
	}
	os.Exit(1)
}
