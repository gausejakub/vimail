package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level represents log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "unknown"
	}
}

// Entry is a single structured log line (JSON Lines format).
type Entry struct {
	Time     string         `json:"time"`
	Level    string         `json:"level"`
	Op       string         `json:"op"`
	Msg      string         `json:"msg"`
	Account  string         `json:"account,omitempty"`
	Folder   string         `json:"folder,omitempty"`
	UID      uint32         `json:"uid,omitempty"`
	Duration string         `json:"duration,omitempty"`
	Error    string         `json:"error,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// Field is a key-value pair for structured context.
type Field struct {
	Key   string
	Value any
}

// Field constructors for ergonomic call sites.
func Acct(email string) Field  { return Field{"account", email} }
func Fld(folder string) Field  { return Field{"folder", folder} }
func MsgUID(uid uint32) Field  { return Field{"uid", uid} }
func Dur(d time.Duration) Field { return Field{"duration", d.Round(time.Millisecond).String()} }
func KV(key string, value any) Field { return Field{key, value} }

func Err(err error) Field {
	if err == nil {
		return Field{}
	}
	return Field{"error", err.Error()}
}

// Logger is the async structured logger.
type Logger struct {
	ch    chan Entry
	file  *os.File
	done  chan struct{}
	level Level
}

const chanSize = 4096

var (
	defaultLogger *Logger
	mu            sync.Mutex
)

// Init creates the global logger. logDir is the directory for vimail.log.
func Init(logDir string, level Level) error {
	mu.Lock()
	defer mu.Unlock()

	if defaultLogger != nil {
		return nil // already initialized
	}

	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}

	rotateIfNeeded(logDir)

	path := filepath.Join(logDir, "vimail.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}

	l := &Logger{
		ch:    make(chan Entry, chanSize),
		file:  f,
		done:  make(chan struct{}),
		level: level,
	}

	go l.drain()
	defaultLogger = l
	return nil
}

// Close flushes remaining entries and closes the log file.
func Close() {
	mu.Lock()
	l := defaultLogger
	defaultLogger = nil
	mu.Unlock()

	if l == nil {
		return
	}
	close(l.ch)
	<-l.done // wait for drain to finish
	l.file.Close()
}

// SetLevel changes the minimum log level at runtime.
func SetLevel(level Level) {
	mu.Lock()
	defer mu.Unlock()
	if defaultLogger != nil {
		defaultLogger.level = level
	}
}

// drain is the single writer goroutine.
func (l *Logger) drain() {
	defer close(l.done)

	enc := json.NewEncoder(l.file)
	enc.SetEscapeHTML(false)

	for entry := range l.ch {
		enc.Encode(entry)
	}
	l.file.Sync()
}

// emit sends an entry to the channel. Non-blocking: drops if full.
func emit(level Level, op, msg string, fields []Field) {
	mu.Lock()
	l := defaultLogger
	mu.Unlock()

	if l == nil || level < l.level {
		return
	}

	e := Entry{
		Time:  time.Now().Format(time.RFC3339Nano),
		Level: level.String(),
		Op:    op,
		Msg:   msg,
	}

	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		switch f.Key {
		case "account":
			e.Account = fmt.Sprint(f.Value)
		case "folder":
			e.Folder = fmt.Sprint(f.Value)
		case "uid":
			if v, ok := f.Value.(uint32); ok {
				e.UID = v
			}
		case "duration":
			e.Duration = fmt.Sprint(f.Value)
		case "error":
			e.Error = fmt.Sprint(f.Value)
		default:
			if e.Extra == nil {
				e.Extra = make(map[string]any, 4)
			}
			e.Extra[f.Key] = f.Value
		}
	}

	// Non-blocking send.
	select {
	case l.ch <- e:
	default:
		// Channel full, drop entry to avoid blocking TUI.
	}
}

// Package-level convenience functions.
func Debug(op, msg string, fields ...Field) { emit(LevelDebug, op, msg, fields) }
func Info(op, msg string, fields ...Field)  { emit(LevelInfo, op, msg, fields) }
func Warn(op, msg string, fields ...Field)  { emit(LevelWarn, op, msg, fields) }
func Error(op, msg string, fields ...Field) { emit(LevelError, op, msg, fields) }

// StdLogWriter returns an io.Writer that feeds log.SetOutput into the structured logger.
type StdLogWriter struct{}

func (StdLogWriter) Write(p []byte) (int, error) {
	Info("stdlib", string(p))
	return len(p), nil
}
