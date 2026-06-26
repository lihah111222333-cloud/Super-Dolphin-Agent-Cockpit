package logger

import (
	"bufio"
	"io"
	"strings"
)

// NewStderrCollector 返回可写入的 stderr 收集器，并异步把子进程 stderr 行转为日志。
// 空行会跳过，包含 error/panic/fatal 的行按 error 级别输出。
func NewStderrCollector(prefix string) io.WriteCloser {
	pr, pw := io.Pipe()
	safeGo("logger.collectStderr", func() { collectStderr(prefix, pr) })
	return pw
}

// collectStderr 扫描 stderr 行并按关键词分级写入全局日志。
func collectStderr(prefix string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		msg := prefix + ": " + line
		if isErrorKeyword(line) {
			Error(msg)
		} else {
			Info(msg)
		}
	}
}

// isErrorKeyword 判断 stderr 行是否应提升为 error 日志。
func isErrorKeyword(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") ||
		strings.Contains(lower, "panic") ||
		strings.Contains(lower, "fatal")
}
