package thread

import (
	"encoding/json"
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

type threadStateKind string

const (
	threadStateStartKind   threadStateKind = "start"
	threadStateResumeKind  threadStateKind = "resume"
	threadStateForkKind    threadStateKind = "fork"
	threadStateRecoverKind threadStateKind = "recover"
)

type threadStateFields struct {
	RequestedThreadID, PublicThreadID, ProviderThreadID, OwnerThreadID string
	AgentID, ParentAgentID, AgentType, AgentMemoryScope                string
	Provider, CWD, Model, Name, Prompt, RolloutPath, SessionUUID       string
	CodexHome, CodexInstanceKey, CodexModelProvider, AgentKey          string
	ConfigOverride                                                     json.RawMessage
	CreatedAt                                                          int64
	PromptVersionID                                                    *int64
	PendingLaunch                                                      bool
}

// newThreadState 组装 thread store 的状态快照。
// 不同来源的 threadID 优先级在这里统一，避免 start、fork、pending 路径写出互相不兼容的公共 ID。
func newThreadState(kind threadStateKind, fields threadStateFields) threadState {
	displayName := strings.TrimSpace(util.FirstNonEmpty(fields.Name, fields.Prompt))
	state := threadState{
		OwnerThreadID:    fields.OwnerThreadID,
		AgentID:          fields.AgentID,
		ParentAgentID:    strings.TrimSpace(fields.ParentAgentID),
		AgentType:        strings.TrimSpace(fields.AgentType),
		AgentMemoryScope: strings.TrimSpace(fields.AgentMemoryScope),
		Provider:         fields.Provider,
		CWD:              fields.CWD,
		Model:            fields.Model,
		Name:             displayName,
		Prompt:           displayName,
	}
	switch kind {
	case threadStateStartKind:
		state.PublicThreadID = util.FirstNonEmpty(fields.PublicThreadID, fields.AgentID)
	case threadStateForkKind:
		state.PublicThreadID = util.FirstNonEmpty(fields.PublicThreadID, fields.ProviderThreadID, fields.AgentID)
	default:
		state.PublicThreadID = util.FirstNonEmpty(fields.PublicThreadID, fields.RequestedThreadID, fields.AgentID)
	}
	// provider_thread_id 只有 provider 返回真实 UUID 后才写入；启动早期允许为空。
	state.ProviderThreadID = strings.TrimSpace(fields.ProviderThreadID)
	state.RolloutPath = fields.RolloutPath
	state.SessionUUID = fields.SessionUUID
	state.ConfigOverride = clone.RawMessage(fields.ConfigOverride)
	state.CodexHome = strings.TrimSpace(fields.CodexHome)
	state.CodexInstanceKey = strings.TrimSpace(fields.CodexInstanceKey)
	state.CodexModelProvider = strings.TrimSpace(fields.CodexModelProvider)
	state.CreatedAt = firstNonZero(fields.CreatedAt)
	state.AgentKey = strings.TrimSpace(fields.AgentKey)
	state.PromptVersionID = fields.PromptVersionID
	state.PendingLaunch = fields.PendingLaunch
	return state
}

func newThreadUpsertParams(thread threadstore.Thread) threadstore.UpsertParams {
	return threadstore.UpsertParams{
		ThreadID:         strings.TrimSpace(thread.ThreadID),
		Name:             strings.TrimSpace(util.FirstNonEmpty(thread.Name, thread.Prompt)),
		Prompt:           strings.TrimSpace(thread.Prompt),
		Model:            strings.TrimSpace(thread.Model),
		Cwd:              strings.TrimSpace(thread.Cwd),
		Status:           strings.TrimSpace(thread.Status),
		Port:             thread.Port,
		PID:              thread.PID,
		CreatedAt:        thread.CreatedAt,
		UpdatedAt:        thread.UpdatedAt,
		OwnerThreadID:    strings.TrimSpace(thread.OwnerThreadID),
		ParentAgentID:    strings.TrimSpace(thread.ParentAgentID),
		AgentType:        strings.TrimSpace(thread.AgentType),
		AgentMemoryScope: strings.TrimSpace(thread.AgentMemoryScope),
		ConfigOverride:   thread.ConfigOverride,
		AgentKey:         strings.TrimSpace(thread.AgentKey),
		PromptVersionID:  thread.PromptVersionID,
		PendingLaunch:    thread.PendingLaunch,
		ManuallyRenamed:  thread.ManuallyRenamed,
	}
}

func newBindingUpsertParams(binding threadBindingRecord) threadBindingUpsertParams {
	return threadBindingUpsertParams{
		AgentID:            strings.TrimSpace(binding.AgentID),
		Provider:           strings.TrimSpace(binding.Provider),
		ProviderThreadID:   strings.TrimSpace(binding.ProviderThreadID),
		CodexThreadID:      strings.TrimSpace(binding.CodexThreadID),
		RolloutPath:        strings.TrimSpace(binding.RolloutPath),
		SessionUUID:        strings.TrimSpace(binding.SessionUUID),
		Cwd:                strings.TrimSpace(binding.Cwd),
		ParentAgentID:      strings.TrimSpace(binding.ParentAgentID),
		AgentType:          strings.TrimSpace(binding.AgentType),
		AgentMemoryScope:   strings.TrimSpace(binding.AgentMemoryScope),
		CreatedAt:          binding.CreatedAt,
		UpdatedAt:          binding.UpdatedAt,
		CodexHome:          strings.TrimSpace(binding.CodexHome),
		CodexInstanceKey:   strings.TrimSpace(binding.CodexInstanceKey),
		CodexModelProvider: strings.TrimSpace(binding.CodexModelProvider),
	}
}

// buildBatchOfflineRuntimeConfig 用本地 DTO 合成批量读取的离线 runtime 配置。
// 这里不调用 store DTO 版本，避免 history 业务路径重新依赖 store 类型。
func buildBatchOfflineRuntimeConfig(stored storedThreadConfig, thread *threadConfigRecord, binding *threadBindingRecord) map[string]any {
	cfg := map[string]any{
		"approvalPolicy": offlineApprovalPolicy,
		"toolRouting": map[string]any{
			"mode":                offlineToolMode,
			"routerModel":         "",
			"routerProvider":      offlineToolProvider,
			"routerBaseURL":       "",
			"routerHasAPIKey":     false,
			"confidenceThreshold": 0.65,
			"timeoutSec":          8,
		},
	}
	cfg = mergeRuntimeConfig(cfg, clone.RuntimeConfigMap(stored.Runtime))
	if thread != nil && strings.TrimSpace(thread.Cwd) != "" {
		cfg["cwd"] = strings.TrimSpace(thread.Cwd)
	} else if binding != nil && strings.TrimSpace(binding.Cwd) != "" {
		cfg["cwd"] = strings.TrimSpace(binding.Cwd)
	}
	if value := strings.TrimSpace(stored.Approvals); value != "" {
		cfg["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(stored.Personality); value != "" {
		cfg["personality"] = value
	}
	if value := strings.TrimSpace(stored.PromptKey); value != "" {
		cfg["promptKey"] = value
		cfg["prompt_key"] = value
	}
	if model := util.FirstNonEmpty(stored.Model, batchThreadModel(thread)); model != "" {
		cfg["model"] = model
	}
	return cfg
}

func batchThreadModel(thread *threadConfigRecord) string {
	if thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.Model)
}

func batchThreadConfigRaw(thread *threadConfigRecord) json.RawMessage {
	if thread == nil {
		return nil
	}
	return thread.ConfigOverride
}

func newStartResult(
	req StartRequest,
	publicThreadID, agentID, providerUUID, providerThreadID, effectiveModel, effectiveCWD string,
) StartResult {
	return StartResult{
		ThreadID:        publicThreadID,
		AgentID:         agentID,
		SessionID:       util.FirstNonEmpty(providerUUID, providerThreadID, publicThreadID),
		Status:          "running",
		Model:           effectiveModel,
		Provider:        req.Provider,
		ModelProvider:   req.ModelProvider,
		CWD:             effectiveCWD,
		ApprovalPolicy:  req.ApprovalPolicy,
		AgentKey:        req.AgentKey,
		AgentTitle:      req.AgentTitle,
		PromptKey:       req.PromptKey,
		PromptVersionID: req.PromptVersionID,
		PromptKeyStale:  req.PromptKeyStale,
	}
}

type threadEventKind string

const (
	threadEventStartedKind      threadEventKind = "started"
	threadEventStoppedKind      threadEventKind = "stopped"
	threadEventMessagesPageKind threadEventKind = "messages_page"
	threadEventCompactedKind    threadEventKind = "compacted"
	threadEventLaunchedKind     threadEventKind = "launched"
)

type threadEventFields struct {
	State                            threadState
	AgentID, Status, Reason, Command string
	TotalCount, Pages                int
	BeforeTokens, AfterTokens        int
	Compacted, Estimated             bool
}

// newThreadEvent 根据线程状态字段构造对外事件 DTO。
// threadID 为空时返回 nil，让调用方在发布前自然跳过无效事件。
func newThreadEvent(kind threadEventKind, threadID string, fields threadEventFields) any {
	header := shareddto.EventHeader{Timestamp: time.Now()}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	switch kind {
	case threadEventStartedKind:
		state := fields.State
		return threaddto.Started{
			EventHeader:      header,
			ThreadID:         threadID,
			AgentID:          strings.TrimSpace(state.AgentID),
			Provider:         strings.TrimSpace(state.Provider),
			ProviderThreadID: strings.TrimSpace(state.ProviderThreadID),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
			Name:             strings.TrimSpace(state.Name),
			PendingLaunch:    state.PendingLaunch,
		}
	case threadEventLaunchedKind:
		state := fields.State
		return threaddto.Launched{
			EventHeader:      header,
			ThreadID:         threadID,
			AgentID:          strings.TrimSpace(state.AgentID),
			Provider:         strings.TrimSpace(state.Provider),
			ProviderThreadID: strings.TrimSpace(state.ProviderThreadID),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
			Name:             strings.TrimSpace(state.Name),
			AgentKey:         strings.TrimSpace(state.AgentKey),
			PromptVersionID:  state.PromptVersionID,
		}
	case threadEventStoppedKind:
		return threaddto.Stopped{
			EventHeader: header,
			ThreadID:    threadID,
			AgentID:     strings.TrimSpace(fields.AgentID),
			Status:      strings.TrimSpace(fields.Status),
			Reason:      strings.TrimSpace(fields.Reason),
		}
	case threadEventMessagesPageKind:
		return threaddto.MessagesPage{
			EventHeader: header,
			ThreadID:    threadID,
			TotalCount:  fields.TotalCount,
			Pages:       fields.Pages,
		}
	case threadEventCompactedKind:
		return threaddto.Compacted{
			EventHeader:  header,
			ThreadID:     threadID,
			Command:      strings.TrimSpace(fields.Command),
			BeforeTokens: fields.BeforeTokens,
			AfterTokens:  fields.AfterTokens,
			Compacted:    fields.Compacted,
			Estimated:    fields.Estimated,
		}
	default:
		return nil
	}
}
