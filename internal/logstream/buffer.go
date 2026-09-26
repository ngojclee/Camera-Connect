package logstream

import (
	"strings"
	"sync"
	"time"
)

// Entry is one buffered log line.
type Entry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

// Buffer stores recent log entries in memory using a bounded ring-like slice.
type Buffer struct {
	mu      sync.RWMutex
	maxSize int
	entries []Entry
	nextID  int64
}

// NewBuffer creates a new log buffer with bounded capacity.
func NewBuffer(maxSize int) *Buffer {
	if maxSize <= 0 {
		maxSize = 500
	}
	return &Buffer{
		maxSize: maxSize,
		entries: make([]Entry, 0, maxSize),
		nextID:  1,
	}
}

// Add appends a log entry to the in-memory buffer.
func (b *Buffer) Add(level, message string) Entry {
	level = normalizeLevel(level)
	message = strings.TrimSpace(message)
	if message == "" {
		message = "-"
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	entry := Entry{
		ID:        b.nextID,
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   message,
	}
	b.nextID++

	b.entries = append(b.entries, entry)
	if len(b.entries) > b.maxSize {
		extra := len(b.entries) - b.maxSize
		b.entries = append([]Entry(nil), b.entries[extra:]...)
	}

	return entry
}

// Since returns entries with ID > afterID up to limit, preserving order.
// Returned cursor is the last entry ID in the returned slice (or afterID if empty).
func (b *Buffer) Since(afterID int64, limit int) ([]Entry, int64) {
	if limit <= 0 {
		limit = 200
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.entries) == 0 {
		return nil, afterID
	}

	start := 0
	for start < len(b.entries) && b.entries[start].ID <= afterID {
		start++
	}
	if start >= len(b.entries) {
		return nil, afterID
	}

	end := start + limit
	if end > len(b.entries) {
		end = len(b.entries)
	}

	out := append([]Entry(nil), b.entries[start:end]...)
	cursor := out[len(out)-1].ID
	return out, cursor
}

// Writer captures standard log lines and appends them into a Buffer.
type Writer struct {
	buf *Buffer
}

// NewWriter creates a new log sink writer.
func NewWriter(buf *Buffer) *Writer {
	return &Writer{buf: buf}
}

// Write implements io.Writer.
func (w *Writer) Write(p []byte) (int, error) {
	if w == nil || w.buf == nil || len(p) == 0 {
		return len(p), nil
	}

	normalized := strings.ReplaceAll(string(p), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		w.buf.Add(inferLevel(line), line)
	}
	return len(p), nil
}

func normalizeLevel(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return "DEBUG"
	case "WARN", "WARNING":
		return "WARN"
	case "ERROR":
		return "ERROR"
	default:
		return "INFO"
	}
}

func inferLevel(line string) string {
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, "[ERROR]"):
		return "ERROR"
	case strings.Contains(upper, "[WARN]"), strings.Contains(upper, "[WARNING]"):
		return "WARN"
	case strings.Contains(upper, "[DEBUG]"):
		return "DEBUG"
	default:
		return "INFO"
	}
}
