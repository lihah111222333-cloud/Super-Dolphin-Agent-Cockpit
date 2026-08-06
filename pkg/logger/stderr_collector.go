package logger

import (
	"bufio"
	"io"
	"strings"
)

// NewStderrCollector 返回可写入的 stderr 收集器，并异步把子进程 stderr 行转为日志。
// 空行会跳过，包含 error/panic/fatal 的行按 error 级别输出。
func (r *Runtime) NewStderrCollector(prefix string) io.WriteCloser {
	if r == nil {
		panic("logger runtime is required")
	}
	pr, pw := io.Pipe()
	r.safeGo("logger.collectStderr", func() { r.collectStderr(prefix, pr) })
	return pw
}

// collectStderr 扫描 stderr 行并按关键词分级写入 runtime 日志。
func (r *Runtime) collectStderr(prefix string, source io.Reader) {
	scanner := bufio.NewScanner(source)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		msg := prefix + ": " + line
		if isErrorKeyword(line) {
			r.Get().Error(msg)
		} else {
			r.Get().Info(msg)
		}
	}
	if err := scanner.Err(); err != nil {
		r.Get().Error("scan child process stderr", "prefix", prefix, "error", err)
	}
}

// isErrorKeyword 判断 stderr 行是否应提升为 error 日志。
func isErrorKeyword(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") ||
		strings.Contains(lower, "panic") ||
		strings.Contains(lower, "fatal")
}
