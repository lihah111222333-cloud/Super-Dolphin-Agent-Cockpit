package codemapindex

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ReadmeCodemap 是代码地图目录表中一行的解析结果，包含 ID、文件名和标题。
type ReadmeCodemap struct {
	ID    string
	File  string
	Title string
}

// readmeTableRowRe 用于从 README 表格行提取 ID、文件名和描述。
var readmeTableRowRe = regexp.MustCompile(`^\|\s*(\d{2})\s*\|\s*\[([^\]]+)\]\([^)]+\)\s*\|\s*(.+?)\s*\|$`)

// SyncREADME 将 codemap 列表同步回 README 的固定表格区间。
// 找不到表格或生成时间区块时保持文件不变，避免误改非标准 README。
func SyncREADME(readmePath string, codemaps []ReadmeCodemap, generatedAt string) error {
	lines, err := readLines(readmePath)
	if err != nil || len(lines) == 0 {
		return err
	}

	tableHeaderIdx, generatedHeadingIdx, ok := locateREADMESections(lines)
	if !ok {
		return nil
	}

	descByFile := extractREADMEDescriptions(lines, tableHeaderIdx, generatedHeadingIdx)
	prefix := rebuildREADMEPrefix(lines[:tableHeaderIdx+2], len(codemaps))
	rebuilt := rebuildREADMEBody(prefix, codemaps, descByFile, generatedAt)
	return os.WriteFile(readmePath, []byte(strings.Join(rebuilt, "\n")), 0644)
}

// readLines 读取 README 并保留行粒度，便于只重建受管表格区域。
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

// locateREADMESections 定位受管表格的表头和生成时间标题。
// ok=false 表示当前 README 不符合生成器预期，调用方应跳过写入。
func locateREADMESections(lines []string) (tableHeaderIdx, generatedHeadingIdx int, ok bool) {
	tableHeaderIdx = -1
	generatedHeadingIdx = -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "| # | 文件 | 覆盖区域 |":
			tableHeaderIdx = i
		case "## 生成时间":
			generatedHeadingIdx = i
		}
	}
	ok = tableHeaderIdx != -1 && generatedHeadingIdx > tableHeaderIdx+1
	return tableHeaderIdx, generatedHeadingIdx, ok
}

// extractREADMEDescriptions 从旧表格行中提取文件名到描述的映射，用于重建时保留已有描述。
func extractREADMEDescriptions(lines []string, tableHeaderIdx, generatedHeadingIdx int) map[string]string {
	descByFile := map[string]string{}
	for _, line := range lines[tableHeaderIdx+2 : generatedHeadingIdx] {
		m := readmeTableRowRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 4 {
			descByFile[m[2]] = strings.TrimSpace(m[3])
		}
	}
	return descByFile
}

// rebuildREADMEPrefix 复制表头前缀并更新卷数描述行，不重写其他人工说明。
func rebuildREADMEPrefix(prefix []string, codemapCount int) []string {
	cloned := append([]string(nil), prefix...)
	for i, line := range cloned {
		if strings.HasPrefix(strings.TrimSpace(line), "> ") {
			cloned[i] = fmt.Sprintf("> 由自动索引脚本维护，当前覆盖 %d 卷核心模块。", codemapCount)
			break
		}
	}
	return cloned
}

// rebuildREADMEBody 拼接受管 README 内容，保留旧描述并刷新生成时间。
func rebuildREADMEBody(prefix []string, codemaps []ReadmeCodemap, descByFile map[string]string, generatedAt string) []string {
	rebuilt := append([]string(nil), prefix...)
	for _, cm := range codemaps {
		rebuilt = append(rebuilt, buildREADMETableRow(cm, descByFile[cm.File]))
	}
	rebuilt = append(rebuilt, "", "## 生成时间", "", generatedAt, "")
	return rebuilt
}

// buildREADMETableRow 格式化单行 Markdown 表格行，描述为空时回退到 cm.Title。
func buildREADMETableRow(cm ReadmeCodemap, desc string) string {
	if desc == "" {
		desc = cm.Title
	}
	return fmt.Sprintf("| %s | [%s](%s) | %s |", cm.ID, cm.File, cm.File, desc)
}
