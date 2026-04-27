//go:build ignore

// check_redaction.go 把 fixture 文件的每一非注释行喂给 DefaultRedactor，
// 断言：每行至少触发一次 [REDACTED:*] 替换；若有任何一行未命中，整体退出码 1。
//
// 用法：go run ./test/fixtures/p21/secrets/check_redaction.go <file>
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check_redaction <fixture>")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()

	r := turn.NewDefaultRedactor()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	miss := 0
	total := 0
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		total++
		out, hits, err := r.Redact(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "line %d: redactor error: %v\n", lineNo, err)
			os.Exit(2)
		}
		if len(hits) == 0 || !strings.Contains(out, "[REDACTED:") {
			fmt.Fprintf(os.Stderr, "MISS line %d: %s\n", lineNo, raw)
			miss++
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	fmt.Printf("checked=%d miss=%d\n", total, miss)
	if miss > 0 {
		os.Exit(1)
	}
}
