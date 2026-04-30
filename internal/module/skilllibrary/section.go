package skilllibrary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UnknownAnchorError is returned when ReadSection cannot resolve the requested
// anchor inside the skill's references directory. It carries the full list of
// anchors that *do* exist so callers (e.g. the skill_read_section host tool)
// can surface them back to the model as correction hints, per spec §7.2.
//
// Unwrap returns fs.ErrNotExist so existing callers using
// errors.Is(err, fs.ErrNotExist) continue to compile and behave the same way.
type UnknownAnchorError struct {
	Name      string
	Anchor    string
	Available []string
}

func (e *UnknownAnchorError) Error() string {
	if len(e.Available) == 0 {
		return fmt.Sprintf("skilllibrary: no section %q in skill %q (skill has no sections)", e.Anchor, e.Name)
	}
	return fmt.Sprintf(
		"skilllibrary: no section %q in skill %q; available anchors: [%s]",
		e.Anchor, e.Name, strings.Join(e.Available, ", "),
	)
}

func (e *UnknownAnchorError) Unwrap() error { return fs.ErrNotExist }

// ReadSection 读取 cacheDir/<name>/references/<NN-anchor>.md。
//
// **Anchor 契约**：anchor 是 H2 标题被 SectionFilename 清洗后的形式（非法字符
// 替换为 "-"，长度截断），不含 "NN-" 数字前缀。后缀匹配 "-<anchor>.md"，因此
// anchor 必须是文件名中数字前缀之后的完整 slug；不允许只传子串（哪怕语义上看
// 起来匹配）。
//
// 错误规约：
//   - cacheDir / name / anchor 任意为空 → 普通 error（非 fs.ErrNotExist）
//   - skill 目录或 references 目录不存在 → fs.ErrNotExist 透传
//   - anchor 在该 skill 下找不到对应文件 → *UnknownAnchorError，Unwrap()→fs.ErrNotExist；
//     Error() 字符串包含该 skill 下所有可用 anchor 列表，便于 agent 修正。
func ReadSection(cacheDir, name, anchor string) ([]byte, error) {
	if cacheDir == "" || name == "" || anchor == "" {
		return nil, errors.New("skilllibrary: ReadSection empty cacheDir/name/anchor")
	}
	refDir := filepath.Join(cacheDir, name, "references")
	entries, err := os.ReadDir(refDir)
	if err != nil {
		return nil, err
	}

	suffix := "-" + anchor + ".md"
	var available []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, suffix) {
			return os.ReadFile(filepath.Join(refDir, n))
		}
		if strings.HasSuffix(n, ".md") {
			if a := anchorFromFilename(n); a != "" {
				available = append(available, a)
			}
		}
	}
	sort.Strings(available)
	return nil, &UnknownAnchorError{Name: name, Anchor: anchor, Available: available}
}

// anchorFromFilename strips a leading "NN-" digit prefix and trailing ".md".
// For example "01-red-green-refactor.md" → "red-green-refactor".
// Returns empty string when the filename does not have any digit prefix
// followed by a dash (treated as malformed; not surfaced to caller).
func anchorFromFilename(filename string) string {
	base := strings.TrimSuffix(filename, ".md")
	i := 0
	for i < len(base) && base[i] >= '0' && base[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(base) || base[i] != '-' {
		return ""
	}
	return base[i+1:]
}
