package claudecli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// fbsdRecorder 是 P6 FBSD 打点的可注入 hook：driver 在 fx init 阶段调
// SetFBSDRecorder 注入 fbsd.Tracker.Record；未注入时 hook 走 no-op。
//
// 使用 atomic.Pointer 避免读写竞态（factory 在 stream-json 解码时高频读，
// 写只发生在 init 阶段一次）。
var fbsdRecorder atomic.Pointer[func(name, anchor string)]

// SetFBSDRecorder 由 driver 在 fx init 调用。fn=nil 时清除 hook（卸载）。
func SetFBSDRecorder(fn func(name, anchor string)) {
	if fn == nil {
		fbsdRecorder.Store(nil)
		return
	}
	fbsdRecorder.Store(&fn)
}

// recordSkillReadIfApplicable 在 Claude tool_use block 是 Read 调用且 path 命中
// workspace skill cache 时，发 FBSD 打点。
//
// 期望路径模式：<*>/.claude/skills/<name>/references/<NN-anchor>.md
// 其他 Read 调用（普通文件、其他工具、非 skills 路径）忽略不打点。
func recordSkillReadIfApplicable(toolName string, input json.RawMessage) {
	ptr := fbsdRecorder.Load()
	if ptr == nil {
		return
	}
	if strings.TrimSpace(toolName) != "Read" {
		return
	}
	name, anchor, ok := parseSkillReadPath(input)
	if !ok {
		return
	}
	(*ptr)(name, anchor)
}

// parseSkillReadPath 从 Read 工具的 input JSON 解析 file_path，识别
// .claude/skills/<name>/references/<NN-anchor>.md 模式并返回 (name, anchor)。
// 不命中或路径异常 → ok=false。
func parseSkillReadPath(input json.RawMessage) (name, anchor string, ok bool) {
	var args struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", "", false
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		return "", "", false
	}
	// Find .claude/skills/<name>/references/ in path
	clean := filepath.ToSlash(filepath.Clean(p))
	idx := strings.Index(clean, "/.claude/skills/")
	if idx < 0 {
		return "", "", false
	}
	rest := clean[idx+len("/.claude/skills/"):]
	parts := strings.Split(rest, "/")
	// 期望 [<name>, "references", "<NN-anchor>.md"]
	if len(parts) < 3 || parts[1] != "references" {
		return "", "", false
	}
	name = parts[0]
	filename := parts[2]
	if !strings.HasSuffix(filename, ".md") {
		return "", "", false
	}
	anchor = anchorFromFilename(filename)
	if anchor == "" {
		return "", "", false
	}
	return name, anchor, true
}

// anchorFromFilename 从 "01-red-green.md" 剥前缀返回 "red-green"。
// 与 skilllibrary.anchorFromFilename 同语义；为避免循环 import 在此重复实现。
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
