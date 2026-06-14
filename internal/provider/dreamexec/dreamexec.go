// Package dreamexec 是 DreamExecutor 真实现的公共子进程执行层。
//
// 设计目的：
//   - claudecli + codexapp 两条 dream provider 的真实现都走 batch CLI 子进程
//     (claude -p --json-schema / codex exec)，差异仅在 binary + args；
//   - 把子进程边界 / stdout cap / stderr 收集 / fence strip / JSON 提取 / retry 抽
//     到公共包，每 provider 仅 ~40 行 thin wrapper；
//   - Commander 接口便于测试注入 fake，避免单测发请求。
//
// 与 dispatcher 的关系：
//   - dispatcher (internal/provider/unified) 已实现 5min timeout / 256KB cap /
//     DREAM_PROVIDER_ORDER env / 5 metrics counter；
//   - 本包专注「单次子进程调用 + 输出整理」，错误透传给 dispatcher 决定 failover。
package dreamexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
)

// ErrBinaryNotAvailable 是 dispatcher 识别 binary 未安装 / 路径不存在的哨兵 error。
// claudecli/codexapp 用 errors.Is 检测后映射到 contract.ErrDreamExecutorNotConfigured
// 让 dispatcher 跳过本 provider 走 failover。
//
// 覆盖三种场景：
//   - PATH 查找失败 (exec.ErrNotFound)
//   - 绝对路径不存在 (*os.PathError + syscall.ENOENT → fs.ErrNotExist)
//   - 兑底字符串检查（未来 stdlib 变化后的防御）
var ErrBinaryNotAvailable = errors.New("dreamexec: binary not available")

// ErrModelUnavailable 表示 provider CLI 可执行，但当前选择的模型不存在或无权限。
// provider wrapper 会映射到 ErrDreamExecutorNotConfigured，让 dispatcher 尝试下一个 provider。
var ErrModelUnavailable = errors.New("dreamexec: model unavailable")

// Commander 抽象一次性子进程执行，便于测试注入 fake。
type Commander interface {
	// Run 执行 binary + args，stdin 写入 input，返回 stdout（受 maxStdoutBytes 限制）。
	// ctx 取消时立即 Kill 子进程。
	// 子进程退出码非 0 → 错误透传 + 携带 stderr 前缀（最多 stderrPreviewBytes 字节）。
	Run(ctx context.Context, binary string, args []string, input string, maxStdoutBytes int64) ([]byte, error)
}

// stderrPreviewBytes 限制错误信息里嵌入的 stderr 预览长度，防爆日志。
const stderrPreviewBytes = 4 * 1024

// realCommander 走 os/exec.CommandContext。
type realCommander struct{}

// NewRealCommander 生产用 commander。
func NewRealCommander() Commander { return realCommander{} }

// Run 启动dreamexec provider后台流程。
func (realCommander) Run(ctx context.Context, binary string, args []string, input string, maxStdoutBytes int64) ([]byte, error) {
	if strings.TrimSpace(binary) == "" {
		return nil, errors.New("dreamexec: binary is empty")
	}
	if maxStdoutBytes <= 0 {
		return nil, fmt.Errorf("dreamexec: maxStdoutBytes must be positive, got %d", maxStdoutBytes)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(input)

	var stdoutBuf, stderrBuf bytes.Buffer
	// 多 1 字节用于检测溢出
	cmd.Stdout = &limitedWriter{w: &stdoutBuf, max: maxStdoutBytes + 1}
	cmd.Stderr = &limitedWriter{w: &stderrBuf, max: stderrPreviewBytes}

	if err := cmd.Run(); err != nil {
		// ctx 取消优先于退出码错误
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// binary 不可用（PATH 未找到 / 绝对路径不存在）转为哨兵 error
		if isBinaryNotAvailable(err) {
			return nil, fmt.Errorf("%w: %s: %v", ErrBinaryNotAvailable, binary, err)
		}
		if modelErr := modelUnavailableErrorFromOutput(stdoutBuf.Bytes(), stderrBuf.Bytes()); modelErr != nil {
			return nil, fmt.Errorf("%w: %s exited with error: %w: %v", ErrModelUnavailable, binary, err, modelErr)
		}
		// 退出码错误携带 stderr 预览
		stderrPreview := strings.TrimSpace(stderrBuf.String())
		if stderrPreview != "" {
			return nil, fmt.Errorf("dreamexec: %s exited with error: %w (stderr: %s)", binary, err, stderrPreview)
		}
		return nil, fmt.Errorf("dreamexec: %s exited with error: %w", binary, err)
	}

	if int64(stdoutBuf.Len()) > maxStdoutBytes {
		return nil, fmt.Errorf("dreamexec: %s stdout exceeded %d bytes", binary, maxStdoutBytes)
	}
	return stdoutBuf.Bytes(), nil
}

// isBinaryNotAvailable 识别 binary 不可用场景：
//   - exec.ErrNotFound: PATH 查找失败（相对名）
//   - fs.ErrNotExist: 绝对路径 fork/exec 换取 ENOENT（*os.PathError）
//   - 字符串 fallback: 防 stdlib 未来变化
func isBinaryNotAvailable(err error) bool {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no such file or directory")
}

// limitedWriter 是带 max 上限的 io.Writer，超过 max 后丢弃但不报错。
// 调用方通过比较实际写入量与 max 检测溢出。
type limitedWriter struct {
	w   io.Writer
	max int64
	n   int64
}

// Write 写入dreamexec provider。
func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.max - lw.n
	if remaining <= 0 {
		// 静默丢弃：调用方检查 lw.n 即可知道溢出
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		written, err := lw.w.Write(p[:remaining])
		lw.n += int64(written)
		// 超量部分静默接受
		return len(p), err
	}
	written, err := lw.w.Write(p)
	lw.n += int64(written)
	return written, err
}

// RunOptions 配置一次 dream 子进程调用。
type RunOptions struct {
	Binary         string
	Args           []string
	Prompt         string
	MaxStdoutBytes int64 // 必须 > 0
	MaxRetries     int   // fence/JSON 解析失败时 retry 次数（建议 0 或 1）

	// OnUsage 在 Run 检测到结构化 CLI 输出（claude envelope / codex JSONL）并提取到非零
	// usage 时被调用。Step 2 入口：wrapper 传入 dreammetrics.AddTokens 路由。
	// nil 或收到零值 usage 时不调用（守门 fallback 路径不污染 counter）。
	OnUsage func(TokenUsage)
}

// Run 执行 commander.Run，推断子进程输出格式后提取 JSON，解析失败时 retry MaxRetries 次。
// 调度顺序：
//  1. extractStructuredCLIResult 探测 raw 是否为 claude envelope / codex JSONL。是且解析成功
//     则拿到 result text + usage；usage 非零且 OnUsage 非 nil 时调用 callback。
//  2. 结构化路径上用 result text 走 StripJSONFences + ExtractFirstJSONObject。
//  3. fallback（未识别为任一 CLI 格式）走原始路径：raw text 直接走 fence strip + JSON 提取。
//
// 子进程错误（exit code / ctx 取消）透传 dispatcher failover，不 retry。
func Run(ctx context.Context, c Commander, opts RunOptions) (string, error) {
	if err := validateRunOptions(c, opts); err != nil {
		return "", err
	}

	var lastParseErr error
	attempts := opts.MaxRetries + 1
	for range attempts {
		obj, usage, err := runDreamAttempt(ctx, c, opts)
		if usage != (TokenUsage{}) && opts.OnUsage != nil {
			opts.OnUsage(usage)
		}
		if err == nil {
			return obj, nil
		}
		if !isParseFailure(err) {
			return "", err
		}
		lastParseErr = err
	}
	return "", fmt.Errorf("dreamexec: failed to extract JSON after %d attempt(s): %w", attempts, lastParseErr)
}

func validateRunOptions(c Commander, opts RunOptions) error {
	if c == nil {
		return errors.New("dreamexec: commander is nil")
	}
	if strings.TrimSpace(opts.Prompt) == "" {
		return errors.New("dreamexec: prompt is empty")
	}
	if opts.MaxStdoutBytes <= 0 {
		return fmt.Errorf("dreamexec: MaxStdoutBytes must be positive, got %d", opts.MaxStdoutBytes)
	}
	if opts.MaxRetries < 0 {
		return fmt.Errorf("dreamexec: MaxRetries must be non-negative, got %d", opts.MaxRetries)
	}
	return nil
}

func runDreamAttempt(ctx context.Context, c Commander, opts RunOptions) (string, TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return "", TokenUsage{}, err
	}
	raw, err := c.Run(ctx, opts.Binary, opts.Args, opts.Prompt, opts.MaxStdoutBytes)
	if err != nil {
		return "", TokenUsage{}, err
	}
	obj, usage, err := extractDreamJSONObject(raw)
	if err != nil {
		return "", usage, parseAttemptError{err: err}
	}
	return obj, usage, nil
}

func extractDreamJSONObject(raw []byte) (string, TokenUsage, error) {
	resultText, usage, structured, err := extractStructuredCLIResult(raw)
	if err != nil {
		return "", usage, err
	}
	if structured {
		obj, err := extractJSONObjectFromText(resultText)
		return obj, usage, err
	}
	obj, err := extractJSONObjectFromText(string(raw))
	return obj, usage, err
}

func extractJSONObjectFromText(text string) (string, error) {
	return ExtractFirstJSONObject(StripJSONFences(text))
}

type parseAttemptError struct {
	err error
}

// Error 返回错误文本。
func (e parseAttemptError) Error() string { return e.err.Error() }

// Unwrap 返回底层错误。
func (e parseAttemptError) Unwrap() error { return e.err }

func isParseFailure(err error) bool {
	var parseErr parseAttemptError
	return errors.As(err, &parseErr)
}
