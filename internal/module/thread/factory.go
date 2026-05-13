package thread

import (
	"encoding/json"
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
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
	RequestedThreadID  string
	PublicThreadID     string
	ProviderThreadID   string
	OwnerThreadID      string
	AgentID            string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	Provider           string
	CWD                string
	Model              string
	Name               string
	Prompt             string
	RolloutPath        string
	SessionUUID        string
	ConfigOverride     json.RawMessage
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
	CreatedAt          int64
	AgentKey           string
	PromptVersionID    *int64
	PendingLaunch      bool
}

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
	// Keep provider_thread_id as-is — empty when the real UUID is not
	// yet known (e.g. Claude resolves it asynchronously after launch).
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

func newBindingUpsertParams(binding bindingstore.Binding) bindingstore.UpsertParams {
	return bindingstore.UpsertParams{
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
		TaskID:          firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake),
		HandoffFile:     firstConfigString(req.Config, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake),
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
	State        threadState
	AgentID      string
	Status       string
	Reason       string
	Command      string
	TotalCount   int
	Pages        int
	BeforeTokens int
	AfterTokens  int
	Compacted    bool
	Estimated    bool
}

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
