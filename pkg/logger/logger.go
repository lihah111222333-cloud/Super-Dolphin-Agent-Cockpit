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

// Mode 表示日志运行模式，用于选择默认级别、输出格式和 source 记录策略。
type Mode string

// 日志运行模式决定默认级别、输出格式和 source 记录策略。
const (
	Production  Mode = "production"
	Development Mode = "development"
	ModeDebug   Mode = "debug"
)

// 内部日志时间格式固定为 UTC+8，兼容现有本地日志阅读习惯。
const (
	logTimeFormat        = "2006-01-02 15:04:05"
	logTimeOffsetSeconds = 8 * 60 * 60
)

// FileOptions 控制文件日志初始化时的文件名前缀和控制台输出。
type FileOptions struct {
	Prefix        string    // 日志文件前缀；为空时使用默认 agent-terminal。
	ConsoleWriter io.Writer // 额外写入的控制台目标；为空时按 mode 选择 stdout/stderr。
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
	logTimeLocation      = time.FixedZone("UTC+8", logTimeOffsetSeconds)
)

// init 初始化默认日志器，保证包级快捷函数在显式 Init 前也可用。
func init() { storeLogger(newLogger(activeMode, activeLevel)) }

// getLogger 返回原子存储的当前全局日志器。
func getLogger() *slog.Logger { return defaultLogger.Load() }

// storeLogger 替换全局日志器并同步 slog 默认实例。
func storeLogger(l *slog.Logger) {
	defaultLogger.Store(l)
	slog.SetDefault(l)
}

// modeFromBuildMode 返回构建期默认运行模式。
func modeFromBuildMode() Mode { return Production }

// normalizeMode 将未知模式收敛为 Production，避免静默进入 debug 输出。
func normalizeMode(mode Mode) Mode {
	switch mode {
	case Development, ModeDebug, Production:
		return mode
	default:
		return Production
	}
}

// defaultLevelForMode 返回模式对应的默认日志级别。
func defaultLevelForMode(mode Mode) slog.Level {
	switch normalizeMode(mode) {
	case Development, ModeDebug:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// parseLevel 解析显式日志级别，无法识别时返回 ok=false。
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

// resolveInitModeAndLevel 解析 Init 入参，支持模式别名和直接传日志级别。
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

// outputWriterForMode 返回默认控制台输出目标；生产走 stdout，开发走 stderr。
func outputWriterForMode(mode Mode) io.Writer {
	if normalizeMode(mode) == Production {
		return os.Stdout
	}
	return os.Stderr
}

// replaceLogAttr 统一清洗日志字段、格式化时间与级别，并映射到 ECS 字段名。
func replaceLogAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.In(logTimeLocation).Format(logTimeFormat))
		}
	case slog.LevelKey:
		a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
	}
	a = sanitizeLogAttr(a)
	return mapECSLogAttr(a)
}

// mapECSLogAttr 将 slog 内置字段和项目 trace/error 字段映射到 ECS 兼容字段名。
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

// newHandler 根据模式构建 JSON 或 text handler，并挂载错误增强与 relay 包装。
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

// newLogger 使用模式默认输出创建全局日志器。
func newLogger(mode Mode, level slog.Level) *slog.Logger {
	return newLoggerWithWriter(mode, level, outputWriterForMode(mode))
}

// newLoggerWithWriter 使用指定 writer 创建带全局字段的日志器。
func newLoggerWithWriter(mode Mode, level slog.Level, out io.Writer) *slog.Logger {
	return applyGlobalAttrs(slog.New(newHandler(mode, level, out)))
}

// Init 根据字符串入参初始化全局日志模式和级别。
func Init(env string) {
	mode, level := resolveInitModeAndLevel(env)
	InitModeWithLevel(mode, level)
}

// InitMode 使用模式默认级别初始化全局日志器。
func InitMode(mode Mode) {
	InitModeWithLevel(mode, defaultLevelForMode(mode))
}

// InitModeWithLevel 设置全局模式和级别；文件 handler 已打开时会重建多路输出。
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

// nextRunNumber 为同一天同前缀日志文件计算递增序号。
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

// InitWithFile 使用默认选项初始化文件日志输出。
func InitWithFile(logDir string) error {
	return InitWithFileOptions(logDir, FileOptions{})
}

// InitWithFileOptions 创建新的文件日志，并关闭旧文件 handler 和 watcher。
// 成功后日志会同时写入控制台和文件，并启动 watchdog 处理文件被删除的情况。
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

// InitWithConsoleWriter 切回仅控制台 writer，并关闭已有文件日志资源。
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

// rebuildLoggerWithFile 在当前模式/级别下重建控制台和文件的多路输出 logger。
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

// SetProject 更新全局 project 字段，并立即重建当前日志器。
func SetProject(name string) {
	logFileMu.Lock()
	globalProject = strings.TrimSpace(name)
	logFileMu.Unlock()
	rebuildActiveLogger()
}

// resolveProjectLogDir 根据 home 和 cwd 推导项目日志目录及项目名。
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
