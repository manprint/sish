package utils

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLogWriterJSON(t *testing.T) {
	var out bytes.Buffer
	w := LogWriter{TimeFmt: "2006", MultiWriter: &out, Format: LogFormatJSON}

	line := "Accepted SSH connection for: 127.0.0.1:1 \"quoted\"\n"
	n, err := w.Write([]byte(line))
	if err != nil || n != len(line) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(line))
	}

	if strings.Count(out.String(), "\n") != 1 || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("want exactly one newline-terminated record, got %q", out.String())
	}

	var rec struct {
		Time, Level, Msg string
	}
	if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
		t.Fatalf("record is not JSON: %v: %q", err, out.String())
	}
	if rec.Level != "INFO" || rec.Msg != strings.TrimSuffix(line, "\n") {
		t.Errorf("unexpected record: %+v", rec)
	}
	if _, err := time.Parse(time.RFC3339Nano, rec.Time); err != nil {
		t.Errorf("time %q is not RFC3339: %v", rec.Time, err)
	}
}

func TestLogWriterTextUnchanged(t *testing.T) {
	var out bytes.Buffer
	w := LogWriter{TimeFmt: "2006", MultiWriter: &out}

	n, err := w.Write([]byte("hello\n"))
	if err != nil || n != len("hello\n") {
		t.Fatalf("Write = %d, %v", n, err)
	}

	if !strings.HasSuffix(out.String(), " | hello\n") || strings.HasPrefix(out.String(), "{") {
		t.Errorf("text format changed: %q", out.String())
	}
}
