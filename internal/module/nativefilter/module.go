package nativefilter

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"go.uber.org/fx"
)

// Module 是 nativefilter 的 fx wire-up 入口，只 Provide *Filter。
// claudecli driver 在 prepareSessionStart 中 inject *Filter 调 Apply。
var Module = fx.Module("nativefilter",
	fx.Provide(NewFilter),
)

// Filter 是 P5 nativefilter 的对外服务。
//
// Apply(workspaceDir) 在每次 spawn Claude CLI 之前调用：读全局基线 +
// 当前 skilllibrary entries 的 ReplacesNative 聚合 + 渲染并写入
// <workspaceDir>/.claude/settings.json。
type Filter struct {
	store  *skilllibrary.Store
	baseFn func() (Config, error)
}

// NewFilter 从 fx 注入 store；baseFn 默认从 ~/.multi-agent/native-cli-filter.json
// 读全局基线（不存在时空 Config）。
func NewFilter(store *skilllibrary.Store) *Filter {
	return &Filter{
		store:  store,
		baseFn: defaultBaseLoader,
	}
}

func defaultBaseLoader() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("nativefilter: user home: %w", err)
	}
	return LoadConfig(filepath.Join(home, ".multi-agent", "native-cli-filter.json"))
}

// Apply 把当前 base config + skilllibrary entries 的 ReplacesNative 聚合渲染
// 并写到 <workspaceDir>/.claude/settings.json。
//
// nil receiver 安全（fx optional inject 场景）；store 为 nil 也 no-op。
func (f *Filter) Apply(workspaceDir string) error {
	if f == nil || f.store == nil {
		return nil
	}
	base, err := f.baseFn()
	if err != nil {
		return fmt.Errorf("nativefilter: load base config: %w", err)
	}
	entries, err := f.store.List()
	if err != nil {
		return fmt.Errorf("nativefilter: list skill entries: %w", err)
	}
	denyExtra := AggregateReplacesNative(entries, "claude")
	allowExtra := AggregateAllowedTools(entries)
	body, err := BuildClaudeSettings(base, denyExtra, allowExtra)
	if err != nil {
		return err
	}
	return WriteWorkspaceSettings(workspaceDir, body)
}
