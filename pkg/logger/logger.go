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

type FileOptions struct {
	Prefix        string
	ConsoleWriter io.Writer
}

var (
	defaultLogger        atomic.Pointer[slog.Logger]
	logFile              *os.File
	logFilePath          string
	logFileConsole       io.Writer
	logFileMu            sync.Mutex
	exitFunc             = os.Exit
	shutdownDBHandler    = func() {}
	globalProject        string
	globalServiceName    = "super-dolphin"
	globalServiceVersion = "dev"
	globalEnv            = "dev"
	activeMode           = modeFromBuildMode()
	activeLevel          = defaultLevelForMode(activeMode)
	defaultLogFilePrefix = "agent-terminal"
)

func init() { storeLogger(newLogger(activeMode, activeLevel)) }

func getLogger() *slog.Logger { return defaultLogger.Load() }

func storeLogger(l *slog.Logger) {
	defaultLogger.Store(l)
	slog.SetDefault(l)
}

func modeFromBuildMode() Mode { return Production }

func normalizeMode(mode Mode) Mode {
	switch mode {
	case Development, ModeDebug, Production:
		return mode
	default:
		return Production
	}
}

func defaultLevelForMode(mode Mode) slog.Level {
	switch normalizeMode(mode) {
	case Development, ModeDebug:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func parseLevel(raw string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// resolveInitModeAndLevel 解析init模式level。
func resolveInitModeAndLevel(raw string) (Mode, slog.Level) {
	value := strings.ToLower(strings.TrimSpace(raw))
	buildMode := modeFromBuildMode()
	switch value {
	case "development", "dev":
		return Development, defaultLevelForMode(Development)
	case "debug":
		return buildMode, slog.LevelDebug
	case "debug-mode":
		return ModeDebug, defaultLevelForMode(ModeDebug)
	case "production", "prod", "release":
		return Production, defaultLevelForMode(Production)
	case "":
		return buildMode, defaultLevelForMode(buildMode)
	default:
		if level, ok := parseLevel(value); ok {
			return buildMode, level
		}
		return buildMode, defaultLevelForMode(buildMode)
	}
}

func outputWriterForMode(mode Mode) io.Writer {
	if normalizeMode(mode) == Production {
		return os.Stdout
	}
	return os.Stderr
}

func replaceLogAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.UTC().Format(time.RFC3339Nano))
		}
	case slog.LevelKey:
		a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
	}
	a = sanitizeLogAttr(a)
	return mapECSLogAttr(a)
}

// mapECSLogAttr 映射ecs日志attr。
func mapECSLogAttr(a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		a.Key = FieldTimestamp
	case slog.LevelKey:
		a.Key = FieldLogLevel
	case slog.MessageKey:
		a.Key = "message"
	case FieldTraceID:
		a.Key = FieldECSTraceID
	case FieldSpanID:
		a.Key = FieldECSSpanID
	case FieldParentSpanID:
		a.Key = FieldECSParentSpanID
	case FieldError:
		a.Key = FieldECSErrorMessage
	case FieldStacktrace:
		a.Key = FieldECSErrorStackTrace
	case FieldDurationMS, FieldLatencyMS:
		a.Key = FieldEventDuration
	}
	return a
}

func newHandler(mode Mode, level slog.Level, out io.Writer) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:       level,
		AddSource:   normalizeMode(mode) != Production,
		ReplaceAttr: replaceLogAttr,
	}
	var handler slog.Handler
	if normalizeMode(mode) == Production {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}
	handler = wrapErrorEnricherHandler(handler, mode)
	return wrapRelayHandler(handler)
}

func newLogger(mode Mode, level slog.Level) *slog.Logger {
	return newLoggerWithWriter(mode, level, outputWriterForMode(mode))
}

func newLoggerWithWriter(mode Mode, level slog.Level, out io.Writer) *slog.Logger {
	return applyGlobalAttrs(slog.New(newHandler(mode, level, out)))
}

// Init 处理init。
func Init(env string) {
	mode, level := resolveInitModeAndLevel(env)
	InitModeWithLevel(mode, level)
}

// InitMode 处理init模式。
func InitMode(mode Mode) {
	InitModeWithLevel(mode, defaultLevelForMode(mode))
}

// InitModeWithLevel 处理带level的init模式。
func InitModeWithLevel(mode Mode, level slog.Level) {
	mode = normalizeMode(mode)
	logFileMu.Lock()
	activeMode = mode
	activeLevel = level
	f := logFile
	logFileMu.Unlock()
	if f != nil {
		rebuildLoggerWithFile(f)
		return
	}
	storeLogger(newLogger(activeMode, activeLevel))
}

func nextRunNumber(logDir, date, prefix string) int {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = defaultLogFilePrefix
	}
	pattern := filepath.Join(logDir, fmt.Sprintf("%s-%s-*.log", prefix, date))
	matches, _ := filepath.Glob(pattern)
	maxN := 0
	base := filepath.Join(logDir, fmt.Sprintf("%s-%s-", prefix, date))
	prefixLen := len(base)
	for _, match := range matches {
		if n, err := strconv.Atoi(match[prefixLen : len(match)-4]); err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN + 1
}

// InitWithFile 处理带文件的init。
func InitWithFile(logDir string) error {
	return InitWithFileOptions(logDir, FileOptions{})
}

// InitWithFileOptions 处理带文件选项的init。
func InitWithFileOptions(logDir string, opts FileOptions) error {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("logger init create log dir: %w", err)
	}

	prefix := strings.TrimSpace(opts.Prefix)
	if prefix == "" {
		prefix = defaultLogFilePrefix
	}
	date := time.Now().Format("2006-01-02")
	run := nextRunNumber(logDir, date, prefix)
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%s-%d.log", prefix, date, run))
	absPath, err := filepath.Abs(logPath)
	if err != nil {
		absPath = logPath
	}

	f, err := os.OpenFile(absPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logger init open log file: %w", err)
	}

	logFileMu.Lock()
	stopFileWatcherLocked()
	closeLogFileLocked()
	logFile = f
	logFilePath = absPath
	logFileConsole = opts.ConsoleWriter
	stopCh := make(chan struct{})
	fileWatcherStop = stopCh
	logFileMu.Unlock()

	rebuildLoggerWithFile(f)
	safeGo("logger.watchLogFile", func() { watchLogFile(absPath, stopCh) })
	Info("log file opened", "path", absPath, "run", run)
	return nil
}

// InitWithConsoleWriter 处理带console写入器的init。
func InitWithConsoleWriter(out io.Writer) {
	if out == nil {
		out = os.Stderr
	}

	logFileMu.Lock()
	stopFileWatcherLocked()
	if logFile != nil {
		closeLogFileLocked()
		logFile = nil
		logFilePath = ""
	}
	logFileConsole = out
	mode := activeMode
	level := activeLevel
	logFileMu.Unlock()

	storeLogger(newLoggerWithWriter(mode, level, out))
}

func rebuildLoggerWithFile(f *os.File) {
	logFileMu.Lock()
	mode := activeMode
	level := activeLevel
	console := logFileConsole
	logFileMu.Unlock()

	if console == nil {
		console = outputWriterForMode(mode)
	}
	writer := io.MultiWriter(console, f)
	storeLogger(newLoggerWithWriter(mode, level, writer))
}

// SetProject 设置项目。
func SetProject(name string) {
	logFileMu.Lock()
	globalProject = strings.TrimSpace(name)
	logFileMu.Unlock()
	rebuildActiveLogger()
}

// resolveProjectLogDir 解析项目日志目录。
func resolveProjectLogDir(homeDir, cwd string) (string, string) {
	logDir := "logs"
	if home := strings.TrimSpace(homeDir); home != "" {
		logDir = filepath.Join(home, ".multi-agent", "log")
	}

	projectName := ""
	if trimmedCwd := strings.TrimSpace(cwd); trimmedCwd != "" {
		projectName = filepath.Base(trimmedCwd)
		if projectName != "" && projectName != "." && projectName != string(filepath.Separator) {
			logDir = filepath.Join(logDir, projectName)
		} else {
			projectName = ""
		}
	}
	return logDir, projectName
}
