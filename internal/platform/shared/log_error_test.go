package shared

import (
	"errors"
	"log/slog"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func requireLoggedError(t *testing.T, value any, want error) {
	t.Helper()

	switch got := value.(type) {
	case error:
		if !errors.Is(got, want) {
			t.Fatalf("logged error = %v, want %v", got, want)
		}
	case string:
		if got != want.Error() {
			t.Fatalf("logged error = %q, want %q", got, want.Error())
		}
	default:
		t.Fatalf("logged error attr = %#v", value)
	}
}

func TestLogIgnoredErrorSkipsNilError(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	logger := pkglogger.New(handler)

	LogIgnoredError(logger, "ignored", nil)
	if handler.Count() != 0 {
		t.Fatalf("log count = %d, want 0", handler.Count())
	}
}

func TestLogIgnoredErrorLogsWarning(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	logger := pkglogger.New(handler)
	wantErr := errors.New("boom")

	LogIgnoredError(logger, "ignored", wantErr)
	if handler.Count() != 1 {
		t.Fatalf("log count = %d, want 1", handler.Count())
	}
	entry := handler.Last()
	if entry.level != slog.LevelWarn || entry.msg != "ignored" {
		t.Fatalf("log = %+v", entry)
	}
	requireLoggedError(t, entry.attrs["error"], wantErr)
}

func TestLogIgnoredErrorAllowsNilLogger(t *testing.T) {
	t.Parallel()

	LogIgnoredError(nil, "ignored", errors.New("boom"))
}
