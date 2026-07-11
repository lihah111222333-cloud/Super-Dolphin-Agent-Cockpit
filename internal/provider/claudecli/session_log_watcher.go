package claudecli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const fallbackSessionLogWatcherPollInterval = 500 * time.Millisecond

func defaultSessionLogWatcherPollInterval() time.Duration {
	return fallbackSessionLogWatcherPollInterval
}

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

type sessionLogPollTarget struct {
	path    string
	modTime time.Time
	offset  int64
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

var errSessionLogWatcherStopped = errors.New("claudecli: session log watcher stopped")

func newSessionLogWatcher(cfg sessionLogWatcherConfig) *sessionLogWatcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultSessionLogWatcherPollInterval()
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
	w.startPollLoop()
}

func (w *sessionLogWatcher) startPollLoop() {
	safego.Go(context.Background(), nil, "claudecli.sessionLogWatcher.pollLoop", func(context.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				if w.logger != nil {
					w.logger.Error("claudecli: recovered session_log_watcher panic", "panic", rec)
				}
			}
		}()
		w.pollLoop()
	})
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

// pollLoop 周期扫描当前 Claude session JSONL 的增量 usage。
// 单次读取错误只写 Debug，watcher 继续运行以覆盖文件稍后出现或被轮换的情况。
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
	target, ok, err := w.pollTarget()
	if err != nil || !ok {
		return err
	}
	return w.pollTargetFile(target)
}

// pollTarget 解析本轮要扫描的日志文件和起始 offset。
// 文件不存在会重置状态但不报错，因为 Claude 可能尚未创建 session JSONL。
func (w *sessionLogWatcher) pollTarget() (sessionLogPollTarget, bool, error) {
	if w == nil || w.resolvePath == nil {
		return sessionLogPollTarget{}, false, nil
	}
	path, err := w.resolvePath()
	if err != nil {
		return sessionLogPollTarget{}, false, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		w.resetFileState()
		return sessionLogPollTarget{}, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return sessionLogPollTarget{}, false, w.handlePollPathError(err)
	}
	return sessionLogPollTarget{
		path:    path,
		modTime: info.ModTime(),
		offset:  w.syncFileState(path, info.Size(), info.ModTime()),
	}, true, nil
}

func (w *sessionLogWatcher) handlePollPathError(err error) error {
	if !os.IsNotExist(err) {
		return err
	}
	w.resetFileState()
	return nil
}

// pollTargetFile 打开目标文件并扫描增量行。
// modTime 未变化时快路径返回，避免无效 Open+Scanner 分配。
func (w *sessionLogWatcher) pollTargetFile(target sessionLogPollTarget) error {
	// modTime 未变化说明文件内容没有更新，跳过 Open+Scanner 避免无效分配。
	w.mu.Lock()
	sameModTime := !target.modTime.IsZero() && target.modTime.Equal(w.modTime) && w.path == target.path && w.offset > 0
	w.mu.Unlock()
	if sameModTime {
		return nil
	}
	file, err := w.openPollFile(target)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	newOffset, err := w.scanPollFile(file)
	if errors.Is(err, errSessionLogWatcherStopped) {
		return nil
	}
	if err != nil {
		return err
	}
	w.setOffset(target.path, newOffset, target.modTime)
	return nil
}

func (w *sessionLogWatcher) openPollFile(target sessionLogPollTarget) (*os.File, error) {
	file, err := os.Open(target.path)
	if err != nil {
		return nil, w.handlePollPathError(err)
	}
	if _, err := file.Seek(target.offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (w *sessionLogWatcher) scanPollFile(file *os.File) (int64, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 100*1024*1024)
	for scanner.Scan() {
		if err := w.dispatchScannedUsage(scanner.Bytes()); err != nil {
			return 0, err
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return file.Seek(0, io.SeekCurrent)
}

// dispatchScannedUsage 解析一行日志并在 usage 变化时回调。
// stopCh 关闭前后都会检查，避免 watcher 停止后仍向 session 派发旧 token 事件。
func (w *sessionLogWatcher) dispatchScannedUsage(raw []byte) error {
	if w.stopped() {
		return errSessionLogWatcherStopped
	}
	usage, ok := parseLogLineUsage(raw)
	if !ok || !w.recordUsage(usage) || w.onUsage == nil {
		return nil
	}
	if w.stopped() {
		return errSessionLogWatcherStopped
	}
	w.onUsage(usage)
	return nil
}

// syncFileState 同步当前文件路径、offset 和 mtime。
// 文件切换、截断或同大小改写都会重置 offset，避免漏读或重复使用旧 usage。
func (w *sessionLogWatcher) syncFileState(path string, size int64, modTime time.Time) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path != path || size < w.offset {
		w.path = path
		w.offset = 0
		w.modTime = modTime
		w.lastUsage = sessionLogUsage{}
		return w.offset
	}
	if size == w.offset && !w.modTime.IsZero() && !w.modTime.Equal(modTime) {
		w.offset = 0
		w.modTime = modTime
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

// parseLogLineUsage 从 Claude assistant 日志行提取 token usage。
// cache creation/read token 会计入输入 token，缺少 input/output 基础字段则忽略该行。
// 使用 json.Unmarshal 而非 json.NewDecoder 避免热循环内每行一次堆分配。
func parseLogLineUsage(raw []byte) (sessionLogUsage, bool) {
	var line map[string]any
	if err := json.Unmarshal(raw, &line); err != nil {
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

// logString 从日志 payload 中按候选 key 提取字符串。
// json.Number 会转成字符串，用于兼容 provider 偶发的数字型 ID。
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

// logInt 从日志 payload 中按候选 key 宽松读取整数。
// 解析成功才返回 ok=true，调用方据此判断必填 usage 字段是否存在。
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
