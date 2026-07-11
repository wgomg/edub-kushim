package utils

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

type LogLevel int

const (
	LevelSilent LogLevel = 1
	LevelFatal  LogLevel = 2
	LevelError  LogLevel = 3
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
		if record.Level >= slog.LevelError {
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

type Logger struct {
	level          LogLevel
	consoleLevel   *slog.LevelVar
	consoleHandler slog.Handler
	fileHandler    slog.Handler
	fileCloser     io.Closer
	RawBodyLog     bool
}

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

func NewDiscardLogger() *Logger {
	consoleLevel := new(slog.LevelVar)
	consoleLevel.Set(slog.Level(13))
	return &Logger{
		level:          LevelInfo,
		consoleLevel:   consoleLevel,
		consoleHandler: slog.DiscardHandler,
	}
}

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

type LogFileConfig struct {
	Path       string
	MaxSize    int
	MaxBackups int
	MaxAge     int
	Compress   bool
}

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
		l.fileCloser.Close()
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
	case LevelFatal:
		return levelFatalSlog
	case LevelSilent:
		return slog.Level(13)
	default:
		return slog.LevelInfo
	}
}

func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
	l.consoleLevel.Set(mapLogLevelToSlog(level))
}

func (l *Logger) Level() LogLevel {
	return l.level
}

func (l *Logger) SlogLogger() *slog.Logger {
	return slog.New(l.consoleHandler)
}

func (l *Logger) log(level slog.Level, pc uintptr, reqID *string, format string, v ...any) {
	msg := format
	if reqID != nil {
		msg = fmt.Sprintf("REQID=%s ", *reqID) + msg
	}
	if len(v) > 0 {
		msg = fmt.Sprintf(msg, v...)
	}

	record := slog.NewRecord(time.Now(), level, msg, pc)

	if l.fileHandler != nil {
		l.fileHandler.Handle(context.Background(), record)
	}
	if l.consoleHandler.Enabled(context.Background(), level) {
		l.consoleHandler.Handle(context.Background(), record)
	}
}

func (l *Logger) Info(reqID *string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelInfo, pc, reqID, format, v...)
}

func (l *Logger) Error(reqID *string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelError, pc, reqID, format, v...)
}

func (l *Logger) Debug(reqID *string, format string, v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	l.log(slog.LevelDebug, pc, reqID, format, v...)
}

func (l *Logger) Fatal(v ...any) {
	var pc uintptr
	pcs := [1]uintptr{}
	if runtime.Callers(2, pcs[:]) > 0 {
		pc = pcs[0]
	}
	msg := fmt.Sprint(v...)
	if l.fileHandler != nil {
		record := slog.NewRecord(time.Now(), levelFatalSlog, msg, pc)
		l.fileHandler.Handle(context.Background(), record)
	}
	if l.consoleHandler.Enabled(context.Background(), levelFatalSlog) {
		record := slog.NewRecord(time.Now(), levelFatalSlog, msg, pc)
		l.consoleHandler.Handle(context.Background(), record)
	}
	os.Exit(1)
}
