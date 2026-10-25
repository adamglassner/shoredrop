package core

import (
	"fmt"
	"sync"
	"time"
)

// LogKind classifies log lines for colouring.
type LogKind int

const (
	LogInfo LogKind = iota
	LogCommand
	LogResponse
	LogSuccess
	LogWarning
	LogError
)

// LogEntry is one line in the transfer log.
type LogEntry struct {
	Time time.Time
	Kind LogKind
	Text string
}

// Log is a bounded, thread-safe activity log.
type Log struct {
	mu      sync.Mutex
	entries []LogEntry
	version int
	OnAdd   func()
}

const maxLog = 5000

func (l *Log) Add(kind LogKind, format string, args ...any) {
	text := format
	if len(args) > 0 {
		text = fmt.Sprintf(format, args...)
	}
	l.mu.Lock()
	l.entries = append(l.entries, LogEntry{time.Now(), kind, text})
	if len(l.entries) > maxLog {
		l.entries = append([]LogEntry(nil), l.entries[len(l.entries)-maxLog*4/5:]...)
	}
	l.version++
	cb := l.OnAdd
	l.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Protocol returns a callback suitable for remote.Params.Log.
func (l *Log) Protocol(prefix string) func(dir, line string) {
	return func(dir, line string) {
		k := LogResponse
		if dir == ">" {
			k = LogCommand
		}
		if prefix != "" {
			line = prefix + " " + line
		}
		l.Add(k, "%s", line)
	}
}

// Snapshot returns a copy of the entries and a version number.
func (l *Log) Snapshot() ([]LogEntry, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LogEntry(nil), l.entries...), l.version
}

// Len returns the number of entries.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *Log) Clear() {
	l.mu.Lock()
	l.entries = nil
	l.version++
	l.mu.Unlock()
	if l.OnAdd != nil {
		l.OnAdd()
	}
}
