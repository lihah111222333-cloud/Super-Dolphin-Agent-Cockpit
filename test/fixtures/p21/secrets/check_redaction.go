//go:build ignore

// check_redaction.go 把 fixture 文件的每一非注释行喂给 DefaultRedactor，
// 断言：每行至少触发一次 [REDACTED:*] 替换；若有任何一行未命中，整体退出码 1。
//
// 用法：go run ./test/fixtures/p21/secrets/check_redaction.go <file>
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
)

// main 运行 fixture 覆盖检查，并把 run 的返回码映射为进程退出码。
func main() {
	if code := run(os.Args); code != 0 {
		os.Exit(code)
	}
}

// run 校验参数、打开 fixture，并保持原有退出码约定：参数或扫描错误为 2，脱敏漏检为 1。
func run(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check_redaction <fixture>")
		return 2
	}
	f, err := os.Open(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer f.Close()

	total, miss, err := scanFixture(f, turn.NewDefaultRedactor())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	fmt.Printf("checked=%d miss=%d\n", total, miss)
	if miss > 0 {
		return 1
	}
	return 0
}

// scanFixture 逐行验证 fixture 样本，注释行和空行不计入覆盖数。
func scanFixture(reader io.Reader, redactor *turn.DefaultRedactor) (int, int, error) {
	scanner := bufio.NewScanner(reader)
	// fixture 可能包含较长的密钥或证书片段，缓冲区必须覆盖整行样本。
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	miss := 0
	total := 0
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		if shouldSkipFixtureLine(raw) {
			continue
		}
		total++
		lineMissed, err := redactFixtureLine(redactor, lineNo, raw)
		if err != nil {
			return total, miss, err
		}
		if lineMissed {
			miss++
		}
	}
	if err := scanner.Err(); err != nil {
		return total, miss, err
	}

	return total, miss, nil
}

// shouldSkipFixtureLine 判断 fixture 行是否只是空白或说明注释。
func shouldSkipFixtureLine(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// redactFixtureLine 对单行样本执行脱敏，并在未命中时保留原有 MISS 输出格式。
func redactFixtureLine(redactor *turn.DefaultRedactor, lineNo int, raw string) (bool, error) {
	out, hits, err := redactor.Redact(raw)
	if err != nil {
		return false, fmt.Errorf("line %d: redactor error: %w", lineNo, err)
	}
	if len(hits) == 0 || !strings.Contains(out, "[REDACTED:") {
		fmt.Fprintf(os.Stderr, "MISS line %d: %s\n", lineNo, raw)
		return true, nil
	}
	return false, nil
}
