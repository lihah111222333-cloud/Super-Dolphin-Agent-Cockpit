package difftracker

import (
	"context"
	"strings"
	"time"
)

// DiffResult 是 toolbridge 对外发布的 diff 事件载荷。
// 字段名保持 JSON wire 兼容，Files 和 DiffText 可能因大小或二进制过滤而只包含可安全展示的子集。
type DiffResult struct {
	AgentID  string   `json:"agentId,omitempty"`
	ThreadID string   `json:"threadId,omitempty"`
	CallID   string   `json:"callId,omitempty"`
	ToolName string   `json:"toolName,omitempty"`
	RepoRoot string   `json:"repoRoot,omitempty"`
	DiffText string   `json:"diffText,omitempty"`
	Files    []string `json:"files,omitempty"`
	Revision int64    `json:"revision,omitempty"`
}

// DiffEmitter 抽象 diff 事件写出边界，调用方负责决定落到事件总线、RPC 或测试桩。
type DiffEmitter func(context.Context, DiffResult) error

// fileDiff 保存单文件 diff 的内部展开形态，保留旧字段名以兼容包内转换逻辑。
type fileDiff struct {
	Path        string
	Before      string
	After       string
	UnifiedDiff string
	Diff        string
}

// FileDiff 是历史公开别名，继续指向内部结构以避免破坏旧调用方编译。
type FileDiff = fileDiff

// beforeFileState 保存工具调用前的 HEAD 和工作区内容。
// tracked/existedBefore 区分新增、删除和未跟踪文件，生成 /dev/null diff 时依赖这两个标记。
type beforeFileState struct {
	path          string
	head          string
	before        string
	tracked       bool
	existedBefore bool
}

// Snapshot 是一次工具调用前的仓库快照。
// RepoRoot/DirtyFiles 对外可读，root/beforeFiles 只供包内重建变更集和过滤超限内容。
type Snapshot struct {
	RepoRoot    string
	DirtyFiles  []string
	root        string
	beforeFiles map[string]beforeFileState
}

// difftracker 的文件数量、单文件大小、总 diff 大小和会话保留默认值。
const (
	MaxTrackedFiles      = 200
	MaxFileSizeBytes     = 1 << 20
	MaxTotalDiffBytes    = 5 << 20
	DefaultSessionTTL    = 30 * time.Minute
	DefaultSweepInterval = time.Minute
)

// isSkippedBinaryExtension 判断扩展名是否不应读取为文本 diff。
func isSkippedBinaryExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".7z", ".a", ".avi", ".bmp", ".class", ".dll", ".dylib", ".exe", ".gif", ".gz",
		".ico", ".jar", ".jpeg", ".jpg", ".mov", ".mp3", ".mp4", ".pdf", ".png", ".so",
		".tar", ".tgz", ".wasm", ".webm", ".webp", ".zip":
		return true
	default:
		return false
	}
}
