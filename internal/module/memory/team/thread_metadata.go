package team

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

type Lifecycle interface {
	StartSession(context.Context, string, contract.BuildCtx) error
	StopSession(context.Context, string) error
}

// BuildCtxFromThreadMetadata 从线程元数据构建ctx。
func BuildCtxFromThreadMetadata(meta *contract.ThreadMetadata, fallbackCWD string) (contract.BuildCtx, bool) {
	buildCtx := contract.BuildCtx{CWD: strings.TrimSpace(fallbackCWD)}
	if meta == nil {
		return buildCtx, buildCtx.CWD != ""
	}
	if cwd := strings.TrimSpace(meta.Cwd); cwd != "" {
		buildCtx.CWD = cwd
	}
	var cfg struct {
		Runtime map[string]any `json:"runtime"`
	}
	if len(meta.ConfigOverride) == 0 || json.Unmarshal(meta.ConfigOverride, &cfg) != nil {
		return buildCtx, buildCtx.CWD != ""
	}
	if gitRoot, ok := cfg.Runtime["gitRoot"].(string); ok {
		buildCtx.GitRoot = strings.TrimSpace(gitRoot)
	}
	if isWorktree, ok := cfg.Runtime["isWorktree"].(bool); ok {
		buildCtx.IsWorktree = isWorktree
	}
	buildCtx.SessionFlags = boolMapValue(cfg.Runtime["sessionFlags"])
	return buildCtx, buildCtx.CWD != ""
}

// boolMapValue 处理boolmap值。
func boolMapValue(raw any) map[string]bool {
	src, ok := raw.(map[string]any)
	if !ok || len(src) == 0 {
		return nil
	}
	out := make(map[string]bool, len(src))
	for key, value := range src {
		flag, ok := value.(bool)
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = flag
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StartSessionFromThreadEvent 从线程事件启动会话。
func StartSessionFromThreadEvent(svc Lifecycle, store contract.ThreadMetadataStore, ev threaddto.Started) error {
	if svc == nil {
		return nil
	}
	buildCtx, ok := contract.BuildCtx{CWD: strings.TrimSpace(ev.CWD)}, strings.TrimSpace(ev.CWD) != ""
	if store != nil && strings.TrimSpace(ev.ThreadID) != "" {
		if meta, err := store.GetByThreadID(context.Background(), ev.ThreadID); err == nil && meta != nil {
			buildCtx, ok = BuildCtxFromThreadMetadata(meta, ev.CWD)
		}
	}
	if !ok {
		return nil
	}
	return svc.StartSession(context.Background(), ev.ThreadID, buildCtx)
}

// StopSessionFromThreadEvent 从线程事件停止会话。
func StopSessionFromThreadEvent(svc Lifecycle, ev threaddto.Stopped) error {
	if svc == nil {
		return nil
	}
	return svc.StopSession(context.Background(), ev.ThreadID)
}
