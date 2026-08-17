package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
)

// ProcessFailureError 是子进程失败的安全摘要；它只保留可审计的低敏计数、
// 退出码和输出摘要，不把可包含密钥、路径或参数值的原始内容放进 Error。
type ProcessFailureError struct {
	// Operation 是稳定的子进程操作逻辑名，不是可执行文件路径。
	Operation string
	// LogicalName 是低敏产品或二进制逻辑名；路径输入会归一为 process。
	LogicalName string
	// ArgsCount 是传给子进程的参数数量，不包含参数值。
	ArgsCount int
	// PackageCount 是 npm 等包安装请求的包数量，不包含包名或版本。
	PackageCount int
	// ExitCodePresent 表示底层错误是否提供了非负退出码。
	ExitCodePresent bool
	// ExitCode 是退出码；ExitCodePresent 为 false 时固定为 -1。
	ExitCode int
	// OutputBytes 是合并 stdout/stderr 的字节数。
	OutputBytes int
	// OutputSHA256 是合并 stdout/stderr 的 SHA-256 十六进制摘要。
	OutputSHA256 string
	// OutputClass 是从 stdout/stderr 提取的固定低敏分类；不保存原始输出、路径或参数。
	OutputClass string
	// OutputSummary 是逐行长度、摘要和固定信号词；不保存原始错误文本。
	OutputSummary string
	cause         error
}

// Error 返回稳定的安全摘要；底层错误只通过 Unwrap 保留给 errors.Is/As，
// 不会因为格式化错误或写入 receipt 而重新暴露原始 stderr、路径或参数。
func (e *ProcessFailureError) Error() string {
	if e == nil {
		return "process execution failed"
	}
	return fmt.Sprintf(
		"%s failed logical_name=%s args_count=%d package_count=%d exit_code_present=%t exit_code=%d output_bytes=%d output_sha256=%s output_class=%s output_summary=%s",
		safeProcessLabel(e.Operation),
		safeProcessLabel(e.LogicalName),
		e.ArgsCount,
		e.PackageCount,
		e.ExitCodePresent,
		e.ExitCode,
		e.OutputBytes,
		e.OutputSHA256,
		safeProcessLabel(e.OutputClass),
		e.OutputSummary,
	)
}

// Unwrap 保留原始执行错误，使调用方仍可用 errors.Is/As 判断超时、取消和退出码。
func (e *ProcessFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// newProcessFailureError 构造唯一的子进程失败摘要；output 只用于长度和 SHA-256，
// 不会存入返回值。调用方必须传入逻辑名，不能把 executable、args 或 env 原样传入。
func newProcessFailureError(operation, logicalName string, cause error, output []byte, argsCount, packageCount int) error {
	if cause == nil {
		cause = errors.New("process execution failed")
	}
	var exitError *exec.ExitError
	exitCode := -1
	exitCodePresent := errors.As(cause, &exitError) && exitError.ExitCode() >= 0
	if exitCodePresent {
		exitCode = exitError.ExitCode()
	}
	digest := sha256.Sum256(output)
	return &ProcessFailureError{
		Operation:       safeProcessLabel(operation),
		LogicalName:     safeProcessLabel(logicalName),
		ArgsCount:       argsCount,
		PackageCount:    packageCount,
		ExitCodePresent: exitCodePresent,
		ExitCode:        exitCode,
		OutputBytes:     len(output),
		OutputSHA256:    hex.EncodeToString(digest[:]),
		OutputClass:     classifyProcessOutput(output),
		OutputSummary:   summarizeProcessOutput(output),
		cause:           cause,
	}
}

// wrapProcessFailure 将可注入 runner 返回的未知错误也收敛到安全摘要；若底层
// 已经是摘要，则复制摘要字段到新的安全外层，避免一个带原始文本的 %w 包装器
// 通过 Error 重新泄漏路径或参数，同时保留完整底层错误链供 errors.Is/As 使用。
func wrapProcessFailure(operation, logicalName string, cause error, argsCount, packageCount int) error {
	if cause == nil {
		return nil
	}
	var summary *ProcessFailureError
	if errors.As(cause, &summary) && summary != nil {
		if argsCount == 0 {
			argsCount = summary.ArgsCount
		}
		if packageCount == 0 {
			packageCount = summary.PackageCount
		}
		return &ProcessFailureError{
			Operation:       safeProcessLabel(operation),
			LogicalName:     safeProcessLabel(logicalName),
			ArgsCount:       argsCount,
			PackageCount:    packageCount,
			ExitCodePresent: summary.ExitCodePresent,
			ExitCode:        summary.ExitCode,
			OutputBytes:     summary.OutputBytes,
			OutputSHA256:    summary.OutputSHA256,
			OutputClass:     summary.OutputClass,
			OutputSummary:   summary.OutputSummary,
			cause:           cause,
		}
	}
	return newProcessFailureError(operation, logicalName, cause, nil, argsCount, packageCount)
}

var processFailureNuGetCodePattern = regexp.MustCompile(`(?i)\bNU[0-9]{4,5}\b`)
var processFailureSDKCodePattern = regexp.MustCompile(`(?i)\bNETSDK[0-9]{4,6}\b`)
var processFailureVersionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+(?:\.[0-9]+){0,2}\b`)
var processFailureSafeFilePattern = regexp.MustCompile(`(?i)\b[A-Za-z0-9._-]+\.(?:nupkg|config|dll|exe|zip)\b`)
var processFailureKnownPackagePattern = regexp.MustCompile(`(?i)\b(?:csharp-ls|CSharpLanguageServer|dotnet|NuGet)\b`)

// classifyProcessOutput 只提取可审计的固定错误类别，避免把 dotnet/NuGet 原始输出写入 receipt。
func classifyProcessOutput(output []byte) string {
	text := strings.ToLower(string(output))
	if code := processFailureNuGetCodePattern.FindString(string(output)); code != "" {
		return strings.ToUpper(code)
	}
	switch {
	case strings.Contains(text, "not found"), strings.Contains(text, "does not exist"):
		return "source_or_package_not_found"
	case strings.Contains(text, "compatible"), strings.Contains(text, "framework"):
		return "sdk_or_framework_incompatible"
	case strings.Contains(text, "restore"), strings.Contains(text, "nuget"):
		return "nuget_restore_failed"
	case strings.Contains(text, "package"):
		return "nuget_package_resolution_failed"
	case strings.Contains(text, "sdk"):
		return "sdk_startup_or_install_failed"
	case strings.Contains(text, "tool"):
		return "dotnet_tool_install_failed"
	case strings.Contains(text, "error"):
		return "tool_error"
	default:
		return "unclassified"
	}
}

// summarizeProcessOutput 生成仅含长度、SHA 和固定词元的逐行证据，避免把 dotnet 输出中的路径、URL、凭据写入错误链。
func summarizeProcessOutput(output []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	parts := make([]string, 0, len(lines))
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		digest := sha256.Sum256([]byte(line))
		signals := make([]string, 0, 8)
		lower := strings.ToLower(line)
		for _, signal := range []string{"nuget", "restore", "package", "source", "framework", "runtime", "sdk", "ssl", "certificate", "access denied", "timeout", "not found", "incompatible", "failed"} {
			if strings.Contains(lower, signal) {
				signals = append(signals, strings.ReplaceAll(signal, " ", "_"))
			}
		}
		if code := processFailureNuGetCodePattern.FindString(line); code != "" {
			signals = append(signals, strings.ToUpper(code))
		}
		if code := processFailureSDKCodePattern.FindString(line); code != "" {
			signals = append(signals, strings.ToUpper(code))
		}
		tokens := make([]string, 0, 6)
		for _, token := range processFailureVersionPattern.FindAllString(line, 4) {
			tokens = append(tokens, "version="+token)
		}
		for _, token := range processFailureSafeFilePattern.FindAllString(line, 4) {
			tokens = append(tokens, "file="+token)
		}
		for _, token := range processFailureKnownPackagePattern.FindAllString(line, 4) {
			tokens = append(tokens, "name="+strings.ToLower(token))
		}
		if len(signals) == 0 {
			signals = append(signals, "unknown")
		}
		parts = append(parts, fmt.Sprintf("line=%d;bytes=%d;sha256=%x;signals=%s;tokens=%s", index+1, len(line), digest[:], strings.Join(signals, ","), strings.Join(tokens, ",")))
	}
	return strings.Join(parts, "|")
}

// joinProcessFailureCause 同时保留命令错误和 context deadline/cancel，使摘要
// 不暴露 context 的原始文本而仍满足 errors.Is(context.DeadlineExceeded)。
func joinProcessFailureCause(contextErr, commandErr error) error {
	if contextErr == nil {
		return commandErr
	}
	if commandErr == nil {
		return contextErr
	}
	return errors.Join(contextErr, commandErr)
}

// safeProcessLabel 只允许低敏逻辑标签；任何路径、盘符、控制符或空白都会被
// 归一为 process，避免错误文本意外携带用户目录或可控参数片段。
func safeProcessLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, `/\:`) {
		return "process"
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "process"
	}
	return raw
}
