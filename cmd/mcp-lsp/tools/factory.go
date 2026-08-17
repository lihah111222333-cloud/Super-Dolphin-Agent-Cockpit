package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// ToolHandler 是 MCP 工具层统一的处理函数类型。
type ToolHandler = middleware.Handler

// Handler 保留旧调用点使用的工具处理器别名。
type Handler = ToolHandler

// decodeMode 控制工具参数按原始、宽松或严格模式解码。
type decodeMode int

// actionHandler 是按 action 分发表中单个动作的处理函数。
type actionHandler[T any] func(context.Context, T) (any, error)

// 解码模式常量决定未知字段、空参数和原始 payload 的处理策略。
const (
	decodeRaw decodeMode = iota
	decodeLenient
	decodeStrict
)

const toolTimeoutDisabled time.Duration = -1

// legacyPositionMigrationHint 从严格解码错误中识别旧版 file_path/line/column 参数。
// 只在命中旧字段时提示改用统一 pos 参数，其他错误保持通用修复建议。
func legacyPositionMigrationHint(err error) string {
	var unknownField *strictToolUnknownFieldError
	if errors.As(err, &unknownField) && isLegacyPositionField(unknownField.Field) {
		return `the inspect/xref/completion tools merged file_path/line/column into a single pos parameter formatted as "file_path:line:column" (example internal/foo.go:42:9)`
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) && isLegacyPositionField(typeError.Field) {
		return `the inspect/xref/completion tools merged file_path/line/column into a single pos parameter formatted as "file_path:line:column" (example internal/foo.go:42:9)`
	}
	return ""
}

func isLegacyPositionField(field string) bool {
	switch field {
	case "file_path", "line", "column":
		return true
	default:
		return false
	}
}

// wrapToolHandler 用 Recovery/Logging/Timeout/Budget 中间件链包装工具处理函数。
func wrapToolHandler(toolName string, tier time.Duration, handler middleware.Handler) middleware.Handler {
	return wrapToolHandlerWithTimeoutResolver(toolName, tier, nil, handler)
}

// wrapToolHandlerWithTimeoutResolver 在统一工作区校验、日志和预算外，允许少数 action 按参数选择或关闭工具层 timeout。
func wrapToolHandlerWithTimeoutResolver(toolName string, tier time.Duration, timeoutTier func(json.RawMessage) time.Duration, handler middleware.Handler) middleware.Handler {
	log := pkglogger.Get()
	scopedHandler := func(ctx context.Context, params json.RawMessage) (any, error) {
		var err error
		ctx = lspinstaller.WithInstallCommandCapability(ctx)
		ctx, err = contextWithExplicitToolWorkDir(ctx, params)
		if err != nil {
			return nil, err
		}
		return handler(ctx, params)
	}
	normalHandler := middleware.Timeout(tier)(scopedHandler)
	slowHandler := middleware.Timeout(middleware.TierSlow)(scopedHandler)
	timedHandler := func(ctx context.Context, params json.RawMessage) (any, error) {
		selected := tier
		if timeoutTier != nil {
			selected = timeoutTier(params)
			if selected == toolTimeoutDisabled {
				return scopedHandler(ctx, params)
			}
			if selected <= 0 {
				selected = tier
			}
		}
		switch selected {
		case tier:
			return normalHandler(ctx, params)
		case middleware.TierSlow:
			return slowHandler(ctx, params)
		default:
			return middleware.Timeout(selected)(scopedHandler)(ctx, params)
		}
	}
	chained := middleware.Chain(
		timedHandler,
		middleware.Recovery(log, toolName),
		middleware.Logging(log, toolName),
	)
	return middleware.WithOutputBudget(toolName, chained, middleware.Budget{})
}

// normalizePlatformWorkDir 把 Windows 主机传给 WSL sidecar 的绝对路径转换成挂载路径（Linux 下执行）。
func normalizePlatformWorkDir(workDir string) string {
	if runtime.GOOS != "linux" {
		return workDir
	}
	return normalizeWSLWorkDir(workDir)
}

func normalizeWSLWorkDir(workDir string) string {
	if len(workDir) < 3 || workDir[1] != ':' || (workDir[2] != '\\' && workDir[2] != '/') {
		return workDir
	}
	drive := workDir[0]
	if drive >= 'A' && drive <= 'Z' {
		drive += 'a' - 'A'
	}
	if drive < 'a' || drive > 'z' {
		return workDir
	}
	remainder := strings.ReplaceAll(workDir[3:], "\\", "/")
	return filepath.Clean("/mnt/" + string(drive) + "/" + remainder)
}

// hostColdInstallOuterTimeoutDisabled 在 Windows 构建中启用锁定资产冷安装策略。
func hostColdInstallOuterTimeoutDisabled() bool {
	return runtime.GOOS == "windows"
}

