package skillforge

import (
	"fmt"
	"regexp"
	"strings"
)

// 文件系统非法字符（POSIX + Windows 共集，含全角冒号）替换为 "-"。
var illegalFilenameChars = regexp.MustCompile(`[/\\:\*\?"<>\|\x{FF1A}]`)

// SectionFilename 按 N1 规则生成 references/<NN-标题>.md 的文件名（仅文件名部分，不含目录）。
//
//   - index：从 1 开始的 H2 出现序号
//   - title：H2 标题原文（保留中文，仅替换非法字符）
//   - 长标题截到 80 runes（保护文件系统兼容性，留出前缀和扩展名）
func SectionFilename(index int, title string) string {
	t := strings.TrimSpace(title)
	t = illegalFilenameChars.ReplaceAllString(t, "-")
	if rc := []rune(t); len(rc) > 80 {
		t = string(rc[:80])
	}
	return fmt.Sprintf("%02d-%s.md", index, t)
}
