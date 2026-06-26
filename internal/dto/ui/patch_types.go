package ui

// PatchActivityStats 承载 thread patch 的轻量活动计数。
type PatchActivityStats struct {
	LSPCalls  int64            `json:"lspCalls"`
	Commands  int64            `json:"commands"`
	FileEdits int64            `json:"fileEdits"`
	ToolCalls map[string]int64 `json:"toolCalls"`
}

// PatchTimelineItem 镜像前端 thread patch 契约，避免 DTO 反向依赖具体模块类型。
type PatchTimelineItem struct {
	ID          string `json:"id"`
	Ts          string `json:"ts"`
	Kind        string `json:"kind"`
	Tool        string `json:"tool,omitempty"`
	Text        string `json:"text,omitempty"`
	Command     string `json:"command,omitempty"`
	File        string `json:"file,omitempty"`
	Status      string `json:"status,omitempty"`
	CallID      string `json:"callId,omitempty"`
	RequestID   int64  `json:"requestId,omitempty"`
	ElapsedMS   *int   `json:"elapsedMs,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Output      string `json:"output,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	Done        bool   `json:"done,omitempty"`
	Internal    bool   `json:"internal,omitempty"`
	Attachments []any  `json:"attachments,omitempty"`
}

// PatchAlert 镜像 thread patch payload 中的前端告警 DTO。
type PatchAlert struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
