package difftracker

import (
	"context"
	"time"
)

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

type DiffEmitter func(context.Context, DiffResult) error

type fileDiff struct {
	Path        string
	Before      string
	After       string
	UnifiedDiff string
	Diff        string
}

type FileDiff = fileDiff

type beforeFileState struct {
	path          string
	head          string
	before        string
	tracked       bool
	existedBefore bool
}

type Snapshot struct {
	RepoRoot    string
	DirtyFiles  []string
	root        string
	beforeFiles map[string]beforeFileState
}

const (
	MaxTrackedFiles      = 200
	MaxFileSizeBytes     = 1 << 20
	MaxTotalDiffBytes    = 5 << 20
	DefaultSessionTTL    = 30 * time.Minute
	DefaultSweepInterval = time.Minute
)

var SkipBinaryExts = map[string]bool{
	".7z":    true,
	".a":     true,
	".avi":   true,
	".bmp":   true,
	".class": true,
	".dll":   true,
	".dylib": true,
	".exe":   true,
	".gif":   true,
	".gz":    true,
	".ico":   true,
	".jar":   true,
	".jpeg":  true,
	".jpg":   true,
	".mov":   true,
	".mp3":   true,
	".mp4":   true,
	".pdf":   true,
	".png":   true,
	".so":    true,
	".tar":   true,
	".tgz":   true,
	".wasm":  true,
	".webm":  true,
	".webp":  true,
	".zip":   true,
}
