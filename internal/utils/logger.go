package utils

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

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

type Logger struct {
	level       LogLevel
	infoLogger  *log.Logger
	errorLogger *log.Logger
	debugLogger *log.Logger
	fatalLogger *log.Logger
	fileLogger  *log.Logger
	RawBodyLog  bool
}

func NewLogger(level string) *Logger {
	logLevel := parseLogLevel(level)

	return &Logger{
		level:       logLevel,
		infoLogger:  log.New(os.Stdout, fmt.Sprintf("<%d>INFO  : ", LevelInfo), log.Ldate|log.Ltime|log.Lshortfile),
		errorLogger: log.New(os.Stderr, fmt.Sprintf("<%d>ERROR : ", LevelError), log.Ldate|log.Ltime|log.Lshortfile),
		debugLogger: log.New(os.Stdout, fmt.Sprintf("<%d>DEBUG : ", LevelDebug), log.Ldate|log.Ltime|log.Lshortfile),
		fatalLogger: log.New(os.Stderr, fmt.Sprintf("<%d>FATAL : ", LevelFatal), log.Ldate|log.Ltime|log.Lshortfile),
	}
}

func NewDiscardLogger() *Logger {
	return &Logger{
		level:       LevelInfo,
		infoLogger:  log.New(io.Discard, "", 0),
		errorLogger: log.New(io.Discard, "", 0),
		debugLogger: log.New(io.Discard, "", 0),
		fatalLogger: log.New(io.Discard, "", 0),
	}
}

func NewLoggerWithWriter(w io.Writer) *Logger {
	return &Logger{
		level:       LevelInfo,
		infoLogger:  log.New(w, fmt.Sprintf("<%d>INFO  : ", LevelInfo), log.Ldate|log.Ltime|log.Lshortfile),
		errorLogger: log.New(w, fmt.Sprintf("<%d>ERROR : ", LevelError), log.Ldate|log.Ltime|log.Lshortfile),
		debugLogger: log.New(w, fmt.Sprintf("<%d>DEBUG : ", LevelDebug), log.Ldate|log.Ltime|log.Lshortfile),
		fatalLogger: log.New(w, fmt.Sprintf("<%d>FATAL : ", LevelFatal), log.Ldate|log.Ltime|log.Lshortfile),
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
	if cfg.MaxSize > 0 {
		writer = &lumberjack.Logger{
			Filename:   cfg.Path,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
	} else {
		f, err := os.OpenFile(cfg.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open log file %s: %w", cfg.Path, err)
		}
		writer = f
	}
	l.fileLogger = log.New(writer, "", log.Ldate|log.Ltime)
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

func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
}

func (l *Logger) Level() LogLevel {
	return l.level
}

func (l *Logger) writeFile(prefix string, format string, v ...any) {
	if l.fileLogger != nil {
		l.fileLogger.Printf(prefix+format, v...)
	}
}

func (l *Logger) Info(reqID *string, format string, v ...any) {
	msg := format
	if reqID != nil {
		msg = fmt.Sprintf("REQID=%s ", *reqID) + msg
	}
	l.writeFile("INFO  : ", msg, v...)

	if LevelInfo > l.level {
		return
	}
	l.infoLogger.Printf(msg, v...)
}

func (l *Logger) Error(reqID *string, format string, v ...any) {
	msg := format
	if reqID != nil {
		msg = fmt.Sprintf("REQID=%s ", *reqID) + msg
	}
	l.writeFile("ERROR : ", msg, v...)

	if LevelError > l.level {
		return
	}
	l.errorLogger.Printf(msg, v...)
}

func (l *Logger) Debug(reqID *string, format string, v ...any) {
	msg := format
	if reqID != nil {
		msg = fmt.Sprintf("REQID=%s ", *reqID) + msg
	}
	l.writeFile("DEBUG : ", msg, v...)

	if LevelDebug > l.level {
		return
	}
	l.debugLogger.Printf(msg, v...)
}

func (l *Logger) Fatal(v ...any) {
	l.writeFile("FATAL : ", fmt.Sprint(v...))
	if LevelFatal > l.level {
		os.Exit(1)
		return
	}
	l.fatalLogger.Fatal(v...)
}
