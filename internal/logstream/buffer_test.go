package logstream

import (
	"strings"
	"testing"
)

func TestBufferSinceWithCursorAndLimit(t *testing.T) {
	buf := NewBuffer(10)
	buf.Add("INFO", "one")
	buf.Add("WARN", "two")
	buf.Add("ERROR", "three")

	entries, cursor := buf.Since(0, 2)
	if len(entries) != 2 {
		t.Fatalf("len(entries)=%d, want 2", len(entries))
	}
	if entries[0].Message != "one" || entries[1].Message != "two" {
		t.Fatalf("unexpected first page messages: %#v", entries)
	}
	if cursor != entries[1].ID {
		t.Fatalf("cursor=%d, want %d", cursor, entries[1].ID)
	}

	next, nextCursor := buf.Since(cursor, 2)
	if len(next) != 1 {
		t.Fatalf("len(next)=%d, want 1", len(next))
	}
	if next[0].Message != "three" {
		t.Fatalf("next[0].Message=%q, want three", next[0].Message)
	}
	if nextCursor != next[0].ID {
		t.Fatalf("nextCursor=%d, want %d", nextCursor, next[0].ID)
	}
}

func TestBufferHonorsMaxSize(t *testing.T) {
	buf := NewBuffer(3)
	buf.Add("INFO", "a")
	buf.Add("INFO", "b")
	buf.Add("INFO", "c")
	buf.Add("INFO", "d")

	entries, _ := buf.Since(0, 10)
	if len(entries) != 3 {
		t.Fatalf("len(entries)=%d, want 3", len(entries))
	}
	if entries[0].Message != "b" || entries[2].Message != "d" {
		t.Fatalf("unexpected retained messages: %#v", entries)
	}
}

func TestWriterSplitsLinesAndInfersLevels(t *testing.T) {
	buf := NewBuffer(10)
	w := NewWriter(buf)

	input := "[INFO] hello\r\n[WARN] world\n[ERROR] boom\n"
	if _, err := w.Write([]byte(input)); err != nil {
		t.Fatalf("write returned error: %v", err)
	}

	entries, _ := buf.Since(0, 10)
	if len(entries) != 3 {
		t.Fatalf("len(entries)=%d, want 3", len(entries))
	}
	if entries[0].Level != "INFO" || entries[1].Level != "WARN" || entries[2].Level != "ERROR" {
		t.Fatalf("unexpected levels: %#v", entries)
	}
	if !strings.Contains(entries[2].Message, "boom") {
		t.Fatalf("expected message to contain boom, got %q", entries[2].Message)
	}
}
