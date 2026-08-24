package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
func Acct(email string) Field        { return Field{"account", email} }
func Fld(folder string) Field        { return Field{"folder", folder} }
func MsgUID(uid uint32) Field        { return Field{"uid", uid} }
func Dur(d time.Duration) Field      { return Field{"duration", d.Round(time.Millisecond).String()} }
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
	level atomic.Int32

	// closeMu guards ch against close-while-sending: emitters hold the read
	// lock across the send, Close takes the write lock to flip closing before
	// closing ch. A bare bool + mutex is not enough because the non-blocking
	// select in emit would still panic if ch were closed mid-send.
	closeMu sync.RWMutex
	closing bool
}

var chanSize = 4096

var (
	defaultLogger atomic.Pointer[Logger]
	mu            sync.Mutex // serializes Init and Close
)

// Init creates the global logger. logDir is the directory for vimail.log.
func Init(logDir string, level Level) error {
	mu.Lock()
	defer mu.Unlock()

	if defaultLogger.Load() != nil {
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
		ch:   make(chan Entry, chanSize),
		file: f,
		done: make(chan struct{}),
	}
	l.level.Store(int32(level))

	go l.drain()
	defaultLogger.Store(l)
	return nil
}

// Close flushes remaining entries and closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()

	l := defaultLogger.Load()
	if l == nil {
		return
	}
	defaultLogger.Store(nil)

	// Block until no emitter is mid-send, then make late emitters (which
	// loaded the pointer before we cleared it) drop instead of sending.
	l.closeMu.Lock()
	l.closing = true
	l.closeMu.Unlock()

	close(l.ch)
	<-l.done // wait for drain to flush accepted entries
	l.file.Close()
}

// SetLevel changes the minimum log level at runtime.
func SetLevel(level Level) {
	if l := defaultLogger.Load(); l != nil {
		l.level.Store(int32(level))
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
	l := defaultLogger.Load()
	if l == nil || level < Level(l.level.Load()) {
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

	// Non-blocking send, holding the read lock so Close cannot close ch
	// between the closing check and the send.
	l.closeMu.RLock()
	if !l.closing {
		select {
		case l.ch <- e:
		default:
			// Channel full, drop entry to avoid blocking TUI.
		}
	}
	l.closeMu.RUnlock()
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
