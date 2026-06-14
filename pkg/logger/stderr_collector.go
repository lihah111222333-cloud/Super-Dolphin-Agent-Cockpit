package logger

import (
	"bufio"
	"io"
	"strings"
)

// NewStderrCollector returns an io.WriteCloser that scans lines from a child
// process stderr and routes them through the global logger. Empty lines are
// skipped; lines containing error/panic/fatal keywords are logged at Error
// level, everything else at Info. Aligned with V2 stderr_collector.go.
// NewStderrCollector 创建stderr收集器。
func NewStderrCollector(prefix string) io.WriteCloser {
	pr, pw := io.Pipe()
	safeGo("logger.collectStderr", func() { collectStderr(prefix, pr) })
	return pw
}

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

func isErrorKeyword(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") ||
		strings.Contains(lower, "panic") ||
		strings.Contains(lower, "fatal")
}
