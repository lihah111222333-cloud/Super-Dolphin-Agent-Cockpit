package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Mode string

const (
	Production  Mode = "production"
	Development Mode = "development"
	ModeDebug   Mode = "debug"
)

var (
	defaultLogger atomic.Pointer[slog.Logger]
	logFile       *os.File
	globalProject string
	activeMode    = Production
	activeLevel   = slog.LevelInfo
	logMu         sync.Mutex
	utc8          = time.FixedZone("UTC+8", 8*60*60)
)

func init() { storeLogger(newLogger(activeMode, activeLevel, nil)) }

func Init(env string) {
	activeMode, activeLevel = resolveInitModeAndLevel(env)
	logMu.Lock()
	f := logFile
	logMu.Unlock()
	if f != nil {
		rebuildLoggerWithFile(f)
		return
	}
	storeLogger(newLogger(activeMode, activeLevel, nil))
}

func InitWithFile(logDir string) error {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	date := time.Now().In(utc8).Format("2006-01-02")
	run := nextRunNumber(logDir, date, "agent-terminal")
	path := filepath.Join(logDir, fmt.Sprintf("agent-terminal-%s-%d.log", date, run))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	logMu.Lock()
	stopFileWatcherLocked()
	closeLogFileLocked()
	logFile = f
	logMu.Unlock()
	rebuildLoggerWithFile(f)
	startFileWatcher(path)
	return nil
}

func SetProject(name string) {
	globalProject = strings.TrimSpace(name)
	logMu.Lock()
	f := logFile
	logMu.Unlock()
	if f != nil {
		rebuildLoggerWithFile(f)
		return
	}
	storeLogger(newLogger(activeMode, activeLevel, nil))
}

func nextRunNumber(logDir, date, prefix string) int {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "agent-terminal"
	}
	pattern := filepath.Join(logDir, fmt.Sprintf("%s-%s-*.log", prefix, date))
	matches, _ := filepath.Glob(pattern)
	base := filepath.Join(logDir, fmt.Sprintf("%s-%s-", prefix, date))
	maxN := 0
	for _, match := range matches {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(match, base), ".log"))
		if err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN + 1
}

func resolveProjectLogDir(homeDir, cwd string) (logDir, projectName string) {
	homeDir = strings.TrimSpace(homeDir)
	cwd = strings.TrimSpace(cwd)
	if homeDir == "" || cwd == "" || cwd == "." {
		return "", ""
	}
	projectName = filepath.Base(cwd)
	if projectName == "" || projectName == "." || projectName == string(filepath.Separator) {
		return "", ""
	}
	return filepath.Join(homeDir, ".multi-agent", "log", projectName), projectName
}

func newHandler(mode Mode, level slog.Level, out io.Writer) slog.Handler {
	if out == nil {
		out = outputWriterForMode(mode)
	}
	opts := &slog.HandlerOptions{Level: level, AddSource: mode != Production, ReplaceAttr: replaceTimeAttr}
	if mode == Production {
		return slog.NewJSONHandler(out, opts)
	}
	return slog.NewTextHandler(out, opts)
}

func newLogger(mode Mode, level slog.Level, out io.Writer) *slog.Logger {
	l := slog.New(newHandler(mode, level, out))
	if globalProject != "" {
		return l.With("project", globalProject)
	}
	return l
}

func rebuildLoggerWithFile(f *os.File) {
	writer := io.MultiWriter(outputWriterForMode(activeMode), f)
	storeLogger(newLogger(activeMode, activeLevel, writer))
}

func storeLogger(l *slog.Logger) {
	defaultLogger.Store(l)
	slog.SetDefault(l)
}

func replaceTimeAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.In(utc8).Format("2006-01-02 15:04:05"))
		}
	}
	return a
}

func resolveInitModeAndLevel(raw string) (Mode, slog.Level) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "development", "dev":
		return Development, slog.LevelDebug
	case "debug":
		return ModeDebug, slog.LevelDebug
	case "production", "prod", "release":
		return Production, slog.LevelInfo
	case "info":
		return Production, slog.LevelInfo
	case "warn", "warning":
		return Production, slog.LevelWarn
	case "error":
		return Production, slog.LevelError
	default:
		return Production, slog.LevelInfo
	}
}

func outputWriterForMode(mode Mode) io.Writer {
	if mode == Production {
		return os.Stdout
	}
	return os.Stderr
}
