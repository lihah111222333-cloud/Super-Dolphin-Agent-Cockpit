package team

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

// Lifecycle 是团队记忆同步服务暴露给线程事件适配层的最小生命周期接口。
type Lifecycle interface {
	StartSession(context.Context, string, contract.BuildCtx) error
	StopSession(context.Context, string) error
}

// BuildCtxFromThreadMetadata 从线程元数据恢复 TeamSync 所需的 BuildCtx。
// ConfigOverride 解析失败时退回 fallbackCWD，避免历史线程元数据格式差异阻断 stop/start 事件处理。
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

// boolMapValue 从 metadata 的动态 JSON map 中提取 bool session flags。
// 非 bool 值会被忽略，避免旧客户端写入的扩展字段污染运行态开关。
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

// StartSessionFromThreadEvent 将 thread.Started 事件转成 TeamSync StartSession 调用。
// 优先读取持久化线程元数据恢复 GitRoot/session flags，读取失败时使用事件自带 CWD 尽力启动。
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

// StopSessionFromThreadEvent 将 thread.Stopped 事件转成 TeamSync StopSession 调用。
// stop 不依赖 BuildCtx，只需要 threadID，因此可在元数据缺失时继续执行最终 flush。
func StopSessionFromThreadEvent(svc Lifecycle, ev threaddto.Stopped) error {
	if svc == nil {
		return nil
	}
	return svc.StopSession(context.Background(), ev.ThreadID)
}
