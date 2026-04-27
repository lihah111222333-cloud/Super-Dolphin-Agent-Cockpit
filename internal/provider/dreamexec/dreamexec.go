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
	"os/exec"
	"strings"
)

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

// limitedWriter 是带 max 上限的 io.Writer，超过 max 后丢弃但不报错。
// 调用方通过比较实际写入量与 max 检测溢出。
type limitedWriter struct {
	w   io.Writer
	max int64
	n   int64
}

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
}

// Run 执行 commander.Run，按 LLM 输出常见格式做 fence strip + JSON 提取，
// 解析失败时 retry MaxRetries 次。子进程错误（exit code / ctx 取消）直接透传。
// 返回值是 sanitized JSON 字符串（保证 ExtractFirstJSONObject 通过）。
func Run(ctx context.Context, c Commander, opts RunOptions) (string, error) {
	if c == nil {
		return "", errors.New("dreamexec: commander is nil")
	}
	if strings.TrimSpace(opts.Prompt) == "" {
		return "", errors.New("dreamexec: prompt is empty")
	}
	if opts.MaxStdoutBytes <= 0 {
		return "", fmt.Errorf("dreamexec: MaxStdoutBytes must be positive, got %d", opts.MaxStdoutBytes)
	}
	if opts.MaxRetries < 0 {
		return "", fmt.Errorf("dreamexec: MaxRetries must be non-negative, got %d", opts.MaxRetries)
	}

	var lastParseErr error
	attempts := opts.MaxRetries + 1
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		raw, err := c.Run(ctx, opts.Binary, opts.Args, opts.Prompt, opts.MaxStdoutBytes)
		if err != nil {
			// 子进程错误：透传，不 retry（dispatcher failover 接管）
			return "", err
		}
		stripped := StripJSONFences(string(raw))
		obj, parseErr := ExtractFirstJSONObject(stripped)
		if parseErr == nil {
			return obj, nil
		}
		lastParseErr = parseErr
	}
	return "", fmt.Errorf("dreamexec: failed to extract JSON after %d attempt(s): %w", attempts, lastParseErr)
}
