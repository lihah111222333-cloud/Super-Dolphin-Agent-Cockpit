package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionLogWatcherPollsAppendedUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	updates := make(chan sessionLogUsage, 4)
	watcher := newSessionLogWatcher(sessionLogWatcherConfig{
		ResolvePath:  func() (string, error) { return path, nil },
		OnUsage:      func(usage sessionLogUsage) { updates <- usage },
		PollInterval: 10 * time.Millisecond,
	})
	watcher.start()
	defer watcher.stopAndWait()

	appendWatcherPollLine(t, path, watcherPollLine("session-1", "2026-04-13T00:00:00Z", 1, 2, 3, 4))
	first := waitWatcherUsage(t, updates)
	if first.InputTokens != 6 || first.OutputTokens != 4 || first.TotalTokens != 10 {
		t.Fatalf("first usage = %#v, want input=6 output=4 total=10", first)
	}

	appendWatcherPollLine(t, path, watcherPollLine("session-1", "2026-04-13T00:00:00Z", 1, 2, 3, 4))
	select {
	case got := <-updates:
		t.Fatalf("duplicate usage dispatched = %#v", got)
	case <-time.After(80 * time.Millisecond):
	}

	appendWatcherPollLine(t, path, watcherPollLine("session-1", "2026-04-13T00:00:01Z", 2, 3, 5, 8))
	second := waitWatcherUsage(t, updates)
	if second.InputTokens != 10 || second.OutputTokens != 8 || second.TotalTokens != 18 {
		t.Fatalf("second usage = %#v, want input=10 output=8 total=18", second)
	}
}

func TestSessionLogWatcherResetsAfterTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	updates := make(chan sessionLogUsage, 4)
	watcher := newSessionLogWatcher(sessionLogWatcherConfig{
		ResolvePath:  func() (string, error) { return path, nil },
		OnUsage:      func(usage sessionLogUsage) { updates <- usage },
		PollInterval: 10 * time.Millisecond,
	})
	if err := os.WriteFile(path, append(watcherPollLine("session-1", "2026-04-13T00:00:00Z", 1, 0, 0, 2), '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	watcher.start()
	defer watcher.stopAndWait()
	first := waitWatcherUsage(t, updates)
	if first.TotalTokens != 3 {
		t.Fatalf("first usage = %#v, want total=3", first)
	}

	if err := os.WriteFile(path, append(watcherPollLine("session-1", "2026-04-13T00:00:01Z", 1, 0, 0, 2), '\n'), 0o644); err != nil {
		t.Fatalf("Rewrite %q error = %v", path, err)
	}
	second := waitWatcherUsage(t, updates)
	if second.Timestamp != "2026-04-13T00:00:01Z" || second.TotalTokens != 3 {
		t.Fatalf("second usage = %#v, want rewritten line to be replayed", second)
	}
}

func TestSessionLogWatcherSameSizeModTimeRescanKeepsDedupState(t *testing.T) {
	line := watcherPollLine("session-1", "2026-04-13T00:00:00Z", 1, 2, 3, 4)
	usage, ok := parseLogLineUsage(line)
	if !ok {
		t.Fatal("parseLogLineUsage() ok = false")
	}

	emitted := 0
	watcher := newSessionLogWatcher(sessionLogWatcherConfig{
		OnUsage: func(sessionLogUsage) { emitted++ },
	})
	watcher.path = "session.jsonl"
	watcher.offset = int64(len(line) + 1)
	watcher.modTime = time.Unix(1, 0)
	watcher.lastUsage = usage

	offset := watcher.syncFileState(watcher.path, watcher.offset, time.Unix(2, 0))
	if offset != 0 {
		t.Fatalf("syncFileState() offset = %d, want rescan from 0", offset)
	}
	if watcher.lastUsage != usage {
		t.Fatalf("lastUsage reset on same-size modTime change: %#v", watcher.lastUsage)
	}
	if err := watcher.dispatchScannedUsage(line); err != nil {
		t.Fatalf("dispatchScannedUsage() error = %v", err)
	}
	if emitted != 0 {
		t.Fatalf("duplicate usage emitted = %d, want 0", emitted)
	}
}

func watcherPollLine(sessionID, timestamp string, input, cacheCreation, cacheRead, output int) []byte {
	raw, _ := json.Marshal(map[string]any{
		"type":      "assistant",
		"sessionId": sessionID,
		"timestamp": timestamp,
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":                input,
				"cache_creation_input_tokens": cacheCreation,
				"cache_read_input_tokens":     cacheRead,
				"output_tokens":               output,
			},
		},
	})
	return raw
}

func appendWatcherPollLine(t *testing.T, path string, raw []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		t.Fatalf("Write(%q) error = %v", path, err)
	}
}

func waitWatcherUsage(t *testing.T, updates <-chan sessionLogUsage) sessionLogUsage {
	t.Helper()
	select {
	case usage := <-updates:
		return usage
	case <-time.After(time.Second):
		t.Fatal("session log watcher did not dispatch usage")
		return sessionLogUsage{}
	}
}
