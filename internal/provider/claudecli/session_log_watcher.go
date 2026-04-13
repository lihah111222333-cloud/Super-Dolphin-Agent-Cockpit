package claudecli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var defaultSessionLogWatcherPollInterval = 500 * time.Millisecond

type sessionLogWatcherConfig struct {
	Logger       *slog.Logger
	ResolvePath  func() (string, error)
	OnUsage      func(sessionLogUsage)
	PollInterval time.Duration
}

type sessionLogUsage struct {
	SessionID    string
	Timestamp    string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type sessionLogWatcher struct {
	logger       *slog.Logger
	resolvePath  func() (string, error)
	onUsage      func(sessionLogUsage)
	pollInterval time.Duration

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	mu        sync.Mutex
	started   bool
	path      string
	offset    int64
	modTime   time.Time
	lastUsage sessionLogUsage
}

func newSessionLogWatcher(cfg sessionLogWatcherConfig) *sessionLogWatcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultSessionLogWatcherPollInterval
	}
	return &sessionLogWatcher{
		logger:       cfg.Logger,
		resolvePath:  cfg.ResolvePath,
		onUsage:      cfg.OnUsage,
		pollInterval: cfg.PollInterval,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

func (w *sessionLogWatcher) start() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()
	go w.pollLoop()
}

func (w *sessionLogWatcher) stopAndWait() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	<-w.doneCh
}

func (w *sessionLogWatcher) pollLoop() {
	defer close(w.doneCh)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.pollOnce(); err != nil && w.logger != nil {
			w.logger.Debug("claudecli: session log watcher poll failed", "error", err)
		}
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func (w *sessionLogWatcher) pollOnce() error {
	if w == nil || w.resolvePath == nil {
		return nil
	}
	path, err := w.resolvePath()
	if err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		w.resetFileState()
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			w.resetFileState()
			return nil
		}
		return err
	}
	offset := w.syncFileState(path, info.Size(), info.ModTime())
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			w.resetFileState()
			return nil
		}
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 100*1024*1024)
	for scanner.Scan() {
		if w.stopped() {
			return nil
		}
		usage, ok := parseLogLineUsage(scanner.Bytes())
		if !ok {
			continue
		}
		if !w.recordUsage(usage) || w.stopped() || w.onUsage == nil {
			continue
		}
		w.onUsage(usage)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	newOffset, err := file.Seek(0, io.SeekCurrent)
	if err == nil {
		w.setOffset(path, newOffset, info.ModTime())
	}
	return nil
}

func (w *sessionLogWatcher) syncFileState(path string, size int64, modTime time.Time) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path != path || size < w.offset || (size == w.offset && !w.modTime.IsZero() && !w.modTime.Equal(modTime)) {
		w.path = path
		w.offset = 0
		w.modTime = modTime
		w.lastUsage = sessionLogUsage{}
	}
	return w.offset
}

func (w *sessionLogWatcher) setOffset(path string, offset int64, modTime time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.path = path
	w.offset = offset
	w.modTime = modTime
}

func (w *sessionLogWatcher) resetFileState() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.path = ""
	w.offset = 0
	w.modTime = time.Time{}
	w.lastUsage = sessionLogUsage{}
}

func (w *sessionLogWatcher) recordUsage(usage sessionLogUsage) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lastUsage == usage {
		return false
	}
	w.lastUsage = usage
	return true
}

func (w *sessionLogWatcher) stopped() bool {
	select {
	case <-w.stopCh:
		return true
	default:
		return false
	}
}

func parseLogLineUsage(raw []byte) (sessionLogUsage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var line map[string]any
	if err := decoder.Decode(&line); err != nil {
		return sessionLogUsage{}, false
	}
	if !strings.EqualFold(logString(line, "type"), "assistant") {
		return sessionLogUsage{}, false
	}
	message, _ := line["message"].(map[string]any)
	usage, _ := message["usage"].(map[string]any)
	if len(usage) == 0 {
		return sessionLogUsage{}, false
	}
	input, ok := logInt(usage, "input_tokens")
	if !ok {
		return sessionLogUsage{}, false
	}
	output, ok := logInt(usage, "output_tokens")
	if !ok {
		return sessionLogUsage{}, false
	}
	cacheCreation, _ := logInt(usage, "cache_creation_input_tokens")
	if cacheCreation == 0 {
		if nested, _ := usage["cache_creation"].(map[string]any); len(nested) > 0 {
			cacheCreation = sumLogInts(nested)
		}
	}
	cacheRead, _ := logInt(usage, "cache_read_input_tokens")
	totalInput := input + cacheCreation + cacheRead
	return sessionLogUsage{
		SessionID:    logString(line, "sessionId", "session_id"),
		Timestamp:    logString(line, "timestamp"),
		InputTokens:  totalInput,
		OutputTokens: output,
		TotalTokens:  totalInput + output,
	}, true
}

func logString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed = strings.TrimSpace(typed); typed != "" {
				return typed
			}
		case json.Number:
			if value := strings.TrimSpace(typed.String()); value != "" {
				return value
			}
		}
	}
	return ""
}

func logInt(payload map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case int:
			return typed, true
		case int64:
			return int(typed), true
		case json.Number:
			parsed, err := typed.Int64()
			return int(parsed), err == nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			return parsed, err == nil
		}
	}
	return 0, false
}

func sumLogInts(payload map[string]any) int {
	total := 0
	for key := range payload {
		if value, ok := logInt(payload, key); ok {
			total += value
		}
	}
	return total
}
