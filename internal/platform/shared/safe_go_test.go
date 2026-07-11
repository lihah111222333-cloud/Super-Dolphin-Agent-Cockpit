package shared

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type recordedLog struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

type recordingHandler struct {
	mu      sync.Mutex
	records []recordedLog
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	entry := recordedLog{
		level: record.Level,
		msg:   record.Message,
		attrs: make(map[string]any),
	}
	record.Attrs(func(attr slog.Attr) bool {
		entry.attrs[attr.Key] = attr.Value.Any()
		return true
	})

	h.mu.Lock()
	h.records = append(h.records, entry)
	h.mu.Unlock()
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func (h *recordingHandler) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.records)
}

func (h *recordingHandler) Last() recordedLog {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) == 0 {
		return recordedLog{}
	}
	return h.records[len(h.records)-1]
}

func waitForChannel(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for goroutine")
	}
}

func waitForLog(t *testing.T, handler *recordingHandler) recordedLog {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if handler.Count() > 0 {
			return handler.Last()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for log")
	return recordedLog{}
}

func TestSafeGoRunsFunction(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	SafeGo(nil, func() {
		close(done)
	})

	waitForChannel(t, done)
}

func TestSafeGoRecoversPanicAndLogs(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	logger := pkglogger.New(handler)
	done := make(chan struct{})

	SafeGo(logger, func() {
		defer close(done)
		// archguard:ignore panic_count -- this test verifies SafeGo panic recovery logging.
		panic("boom")
	})

	waitForChannel(t, done)
	entry := waitForLog(t, handler)
	if entry.level != slog.LevelError || entry.msg != "recovered panic in goroutine" {
		t.Fatalf("log = %+v", entry)
	}
	if entry.attrs["panic"] != "boom" {
		t.Fatalf("panic attr = %#v", entry.attrs["panic"])
	}
	stack, ok := entry.attrs["stack"].(string)
	if !ok || stack == "" {
		t.Fatalf("stack attr = %#v", entry.attrs["stack"])
	}
}

func TestSafeGoRecoversPanicWithNilLogger(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	SafeGo(nil, func() {
		defer close(done)
		// archguard:ignore panic_count -- this test verifies SafeGo nil-logger panic recovery.
		panic("boom")
	})

	waitForChannel(t, done)
}
