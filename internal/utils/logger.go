package utils

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
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
	RawBodyLog  bool
}

func NewLogger(level string) *Logger {
	logLevel := parseLogLevel(level)

	return &Logger{
		level:       logLevel,
		infoLogger:  log.New(os.Stdout, fmt.Sprintf("<%d>INFO  : ", LevelInfo), log.Ldate|log.Ltime|log.Lshortfile),
		errorLogger: log.New(os.Stderr, fmt.Sprintf("<%d>ERROR  : ", LevelError), log.Ldate|log.Ltime|log.Lshortfile),
		debugLogger: log.New(os.Stdout, fmt.Sprintf("<%d>DEBUG  : ", LevelDebug), log.Ldate|log.Ltime|log.Lshortfile),
		fatalLogger: log.New(os.Stderr, fmt.Sprintf("<%d>FATAL  : ", LevelFatal), log.Ldate|log.Ltime|log.Lshortfile),
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
		errorLogger: log.New(w, fmt.Sprintf("<%d>ERROR  : ", LevelError), log.Ldate|log.Ltime|log.Lshortfile),
		debugLogger: log.New(w, fmt.Sprintf("<%d>DEBUG  : ", LevelDebug), log.Ldate|log.Ltime|log.Lshortfile),
		fatalLogger: log.New(w, fmt.Sprintf("<%d>FATAL  : ", LevelFatal), log.Ldate|log.Ltime|log.Lshortfile),
	}
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

func (l *Logger) Info(reqID *string, format string, v ...any) {
	if LevelInfo > l.level {
		return
	}

	if reqID != nil {
		format = fmt.Sprintf("REQID=%s ", *reqID) + format
	}

	l.infoLogger.Printf(format, v...)
}

func (l *Logger) Error(reqID *string, format string, v ...any) {
	if LevelError > l.level {
		return
	}

	if reqID != nil {
		format = fmt.Sprintf("REQID=%s ", *reqID) + format
	}

	l.errorLogger.Printf(format, v...)
}

func (l *Logger) Debug(reqID *string, format string, v ...any) {
	if LevelDebug > l.level {
		return
	}

	if reqID != nil {
		format = fmt.Sprintf("REQID=%s ", *reqID) + format
	}
	l.debugLogger.Printf(format, v...)
}

func (l *Logger) Fatal(v ...any) {
	if LevelFatal > l.level {
		os.Exit(1)
		return
	}

	l.fatalLogger.Fatal(v...)
}
