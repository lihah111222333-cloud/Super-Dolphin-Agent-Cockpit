package app

import (
	"context"
	"os"
	"path/filepath"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
	"github.com/anthropic-ai/super-agent-v3/internal/module/cron"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/fbsd"
	"github.com/anthropic-ai/super-agent-v3/internal/module/feedback"
	"github.com/anthropic-ai/super-agent-v3/internal/module/insight"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	"github.com/anthropic-ai/super-agent-v3/internal/module/notify"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	turnobservation "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/cachekeepalive"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/hooks"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/store"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

// Module wires the core app surface. It intentionally exposes the ctl control plane
// for externally started MCP binaries, but does not launch or supervise MCP processes.
var Module = fx.Options(
	fx.Provide(NewLogger),
	fx.Provide(pidregistry.New),
	config.Module,
	db.Module,
	bus.Module,
	rpc.Module,
	hooks.Module,
	cachekeepalive.Module,
	mcpcontrol.Module,
	platformrunner.Module,
	statemachine.Module,
	store.Module,
	dashboard.Module,
	feedback.Module,
	memory.Module,
	prompt.Module,
	skill.Module,
	skillforge.Module,
	skilllibrary.Module,
	fbsd.Module,
	fx.Provide(provideSkillLibraryConfig),
	fx.Provide(provideContractSkillLibraryConfig),
	fx.Provide(provideFBSDRecorder),
	fx.Provide(provideWorkspaceSkillSetup),
	fx.Provide(provideSkillManifestRenderer),
	fx.Invoke(initProviderHooks),
	thread.Module,
	turn.Module,
	turnobservation.Module,
	// P21 模块：P1b cron 计划任务、P2 多平台通知、P3 session insights。
	// 三者均采用 fx optional 依赖，缺少上游依赖时自动降级为 noop，不阻塞整体装配图闭合。
	cron.Module,
	notify.Module,
	insight.Module,
	uistate.Module,
	unified.Module,
	claudecli.Module,
	codexapp.Module,
	toolbridge.Module, // P15 新增：始终加载
	ToolbridgeAdapters,
	ToolbridgeCodexBinding,
	// orchestration is handled entirely by the standalone mcp-orch MCP server;
	// the desktop app must NOT embed its own orchestration module, otherwise
	// localLauncher re-spawns the desktop binary as a subprocess which exits
	// immediately and causes agent state to go to "failed".
	fx.Provide(
		AsRPCRunner,
		newThreadOrchestrationFacade,
		newRuntimeReporter,
		provideNativeToolDescriptors,
		provideDisabledBuiltinToolsFn,
	),
)

func provideSkillLibraryConfig() skilllibrary.Config {
	home, _ := os.UserHomeDir()
	return skilllibrary.Config{
		LibraryDir:     filepath.Join(home, ".multi-agent", "skills-library"),
		CacheDir:       filepath.Join(home, ".multi-agent", "skills-cache"),
		HarnessVersion: "dev",
	}
}

func provideNativeToolDescriptors(registry *unified.Registry) []contract.NativeToolDescriptor {
	if registry == nil {
		return nil
	}
	return registry.NativeTools()
}

// provideDisabledBuiltinToolsFn bridges uistate soft-filter resolution into
// prompt.DisabledBuiltinToolsFn without creating a prompt→uistate import cycle.
func provideDisabledBuiltinToolsFn(prefs uipreference.Store, tools []contract.NativeToolDescriptor) prompt.DisabledBuiltinToolsFn {
	index := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, t := range tools {
		index[t.ID] = t
	}
	return func(ctx context.Context, cwd string) []string {
		return uistate.ResolveSoftFilteredBuiltinTools(ctx, prefs, cwd, tools, index)
	}
}

func AsRPCRunner(server *rpc.Server) RunnerResult {
	return RunnerResult{Runner: server}
}

// provideContractSkillLibraryConfig bridges skilllibrary.Config to the
// contract-level type that provider packages consume.
func provideContractSkillLibraryConfig(cfg skilllibrary.Config) contract.SkillLibraryConfig {
	return contract.SkillLibraryConfig{CacheDir: cfg.CacheDir}
}

// provideFBSDRecorder exposes *fbsd.Tracker as contract.FBSDRecorder.
// The tracker may be nil (fx optional); callers must nil-check.
func provideFBSDRecorder(t *fbsd.Tracker) contract.FBSDRecorder {
	if t == nil {
		return nil
	}
	return t
}

// provideWorkspaceSkillSetup exposes cliadapter.SetupWorkspaceSkills as a
// contract.WorkspaceSkillSetupFunc for the claudecli provider.
func provideWorkspaceSkillSetup() contract.WorkspaceSkillSetupFunc {
	return cliadapter.SetupWorkspaceSkills
}

// provideSkillManifestRenderer creates the contract.SkillManifestRenderer
// using the fbsd ManifestRenderer implementation.
func provideSkillManifestRenderer(entries contract.SkillManifestEntryLister, descriptions contract.SkillDescriptionParser, tracker *fbsd.Tracker) contract.SkillManifestRenderer {
	if entries == nil {
		return nil
	}
	return fbsd.NewManifestRenderer(entries, descriptions, tracker)
}

// initProviderHooks wires module-layer functions into provider/shared hooks
// so the provider layer can use CaptureToolResult, ResetToolResultScope, and
// TrimInjectedSkillBlocks without importing module packages directly.
func initProviderHooks() {
	providershared.SetCaptureToolResultHook(func(meta providershared.ToolResultMeta, raw string) providershared.ToolResultRecord {
		result := turn.CaptureToolResult(turn.ToolResultMeta{
			ThreadID:  meta.ThreadID,
			TurnID:    meta.TurnID,
			CallID:    meta.CallID,
			ToolName:  meta.ToolName,
			Timestamp: meta.Timestamp,
		}, raw)
		return providershared.ToolResultRecord{
			Preview:       result.Preview,
			PersistedPath: result.PersistedPath,
			Truncated:     result.Truncated,
			OriginalSize:  result.OriginalSize,
		}
	})
	providershared.SetResetToolResultScopeHook(turn.ResetToolResultScope)
	providershared.SetTrimSkillBlocksHook(skill.TrimInjectedSkillBlocks)
}
