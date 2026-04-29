package nativefilter

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"go.uber.org/fx"
)

// envFlag 是 P5 灰度开关。P5 默认关：写错配置不影响现网，spec §12 + §13 #10
// 灰度规范要求；显式设为 "on" 才会真的写 settings.json。
const envFlag = "SUPER_DOLPHIN_NATIVE_FILTER"

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
//
// Feature flag 关闭（默认或 != "on"）时直接 no-op 返回 nil，driver 端不必
// 再判 flag。
type Filter struct {
	store   *skilllibrary.Store
	baseFn  func() (Config, error)
	enabled bool
}

// NewFilter 从 fx 注入 store；baseFn 默认从 ~/.multi-agent/native-cli-filter.json
// 读全局基线（不存在时空 Config）。
func NewFilter(store *skilllibrary.Store) *Filter {
	return &Filter{
		store:   store,
		baseFn:  defaultBaseLoader,
		enabled: os.Getenv(envFlag) == "on",
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
// 并写到 <workspaceDir>/.claude/settings.json。Feature flag 关闭时直接 no-op。
//
// nil receiver 安全（fx optional inject 场景）。
func (f *Filter) Apply(workspaceDir string) error {
	if f == nil || !f.enabled || f.store == nil {
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
	extra := AggregateReplacesNative(entries, "claude")
	body, err := BuildClaudeSettings(base, extra)
	if err != nil {
		return err
	}
	return WriteWorkspaceSettings(workspaceDir, body)
}

// Enabled 报告 feature flag 是否打开（用于 driver 集成时的诊断日志）。
func (f *Filter) Enabled() bool {
	return f != nil && f.enabled
}
