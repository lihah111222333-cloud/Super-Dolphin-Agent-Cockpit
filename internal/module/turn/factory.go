package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

type prepareInputSpec struct {
	Inputs                       []InputItem
	Prompt                       string
	Images                       []string
	Files                        []string
	CandidateSkills              []dto.SkillRef
	ManualSkillSelection         bool
	Provider                     string
	Model                        string
	Effort                       string
	OutputSchema                 json.RawMessage
	AgentID                      string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  contract.MCPSnapshot
	SessionFlags                 map[string]bool
	OutputStyleConfig            *contract.OutputStyleConfig
	ScratchpadDir                string
	ThreadRuntimeConfig          map[string]any
	BinaryDir                    string
}

type prepareSkillSpec struct {
	Selected []string
	Derived  []string
}

type prepareInputSession interface {
	Capabilities() dto.CapabilitySet
}

type runtimeConfigSnapshotReader interface {
	RuntimeConfigSnapshot() map[string]any
}

func buildPrepareInput(spec prepareInputSpec, skills prepareSkillSpec, session prepareInputSession) PrepareInput {
	var caps dto.CapabilitySet
	if session != nil {
		caps = session.Capabilities()
	}
	input := PrepareInput{
		Inputs:                       append([]InputItem(nil), spec.Inputs...),
		Prompt:                       spec.Prompt,
		Images:                       append([]string(nil), spec.Images...),
		Files:                        append([]string(nil), spec.Files...),
		Skills:                       normalizeSkillNames(skills.Selected, skills.Derived),
		CandidateSkills:              cloneSkillRefs(spec.CandidateSkills),
		ManualSkillSelection:         spec.ManualSkillSelection,
		Provider:                     strings.TrimSpace(spec.Provider),
		Model:                        strings.TrimSpace(spec.Model),
		Effort:                       spec.Effort,
		OutputSchema:                 append(json.RawMessage(nil), spec.OutputSchema...),
		AgentID:                      strings.TrimSpace(spec.AgentID),
		CWD:                          strings.TrimSpace(spec.CWD),
		GitRoot:                      strings.TrimSpace(spec.GitRoot),
		IsWorktree:                   spec.IsWorktree,
		Language:                     strings.TrimSpace(spec.Language),
		EnabledTools:                 append([]string(nil), spec.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), spec.AdditionalWorkingDirectories...),
		MCPSnapshot:                  cloneMCPSnapshot(spec.MCPSnapshot),
		SessionFlags:                 clonePrepareFlags(spec.SessionFlags),
		OutputStyleConfig:            cloneOutputStyleConfigValue(spec.OutputStyleConfig),
		ScratchpadDir:                strings.TrimSpace(spec.ScratchpadDir),
		ThreadRuntimeConfig:          platformshared.CloneRuntimeConfigMap(spec.ThreadRuntimeConfig),
		ThreadCaps:                   caps,
		BinaryDir:                    spec.BinaryDir,
	}
	return hydratePrepareInput(input, session)
}

func hydratePrepareInput(input PrepareInput, session prepareInputSession) PrepareInput {
	input = mergePrepareInputRuntime(input, input.ThreadRuntimeConfig)
	reader, ok := session.(runtimeConfigSnapshotReader)
	if !ok {
		return input
	}
	return mergePrepareInputRuntime(input, reader.RuntimeConfigSnapshot())
}

func mergePrepareInputRuntime(input PrepareInput, cfg map[string]any) PrepareInput {
	if len(cfg) == 0 {
		return input
	}
	input.Provider = platformshared.FirstNonEmpty(strings.TrimSpace(input.Provider), providershared.ConfigString(cfg, "provider"))
	input.CWD = platformshared.FirstNonEmpty(strings.TrimSpace(input.CWD), providershared.ConfigString(cfg, "cwd"))
	input.Model = platformshared.FirstNonEmpty(strings.TrimSpace(input.Model), providershared.ConfigString(cfg, "model"))
	input.GitRoot = platformshared.FirstNonEmpty(strings.TrimSpace(input.GitRoot), providershared.ConfigString(cfg, "gitRoot", "git_root"))
	input.IsWorktree = input.IsWorktree || configBool(cfg, "isWorktree", "is_worktree")
	input.Language = platformshared.FirstNonEmpty(strings.TrimSpace(input.Language), providershared.ConfigString(cfg, "language"))
	input.EnabledTools = firstNonEmptyStrings(input.EnabledTools, providershared.ConfigStringSlice(cfg, "enabledTools", "enabled_tools", "tools"))
	input.AdditionalWorkingDirectories = firstNonEmptyStrings(input.AdditionalWorkingDirectories, providershared.ConfigStringSlice(cfg, "additionalWorkingDirectories", "additional_working_directories"))
	input.MCPSnapshot = mergeMCPSnapshot(input.MCPSnapshot, configMCPSnapshot(cfg))
	input.SessionFlags = firstNonEmptyFlags(input.SessionFlags, configBoolMap(cfg, "sessionFlags", "session_flags"))
	input.OutputStyleConfig = firstNonNilOutputStyle(input.OutputStyleConfig, configOutputStyle(cfg, "outputStyleConfig", "output_style_config"))
	input.ScratchpadDir = platformshared.FirstNonEmpty(strings.TrimSpace(input.ScratchpadDir), configScratchpadDir(cfg, "scratchpadDir", "scratchpad_dir"))
	return input
}

func configBool(cfg map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := cfg[key].(bool); ok {
			return value
		}
	}
	return false
}

func configBoolMap(cfg map[string]any, keys ...string) map[string]bool {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if flags := normalizePrepareBoolMap(value); len(flags) > 0 {
			return flags
		}
	}
	return nil
}

func normalizePrepareBoolMap(value any) map[string]bool {
	switch typed := value.(type) {
	case map[string]bool:
		return clonePrepareFlags(typed)
	case map[string]any:
		out := make(map[string]bool, len(typed))
		for key, raw := range typed {
			flag, ok := raw.(bool)
			if ok {
				key = strings.TrimSpace(key)
				if key != "" {
					out[key] = flag
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func configMCPSnapshot(cfg map[string]any) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:      providershared.ConfigStringSlice(cfg, "mcpServers", "mcp_servers"),
		Tools:        providershared.ConfigStringSlice(cfg, "mcpTools", "mcp_tools"),
		Instructions: configStringMap(cfg, "mcpInstructions", "mcp_instructions"),
	}
}

func configStringMap(cfg map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if out := providershared.StringMap(value); len(out) > 0 {
			return out
		}
	}
	return nil
}

func firstNonEmptyStrings(primary, fallback []string) []string {
	if out := providershared.NormalizeConfigStringSlice(primary); len(out) > 0 {
		return out
	}
	if out := providershared.NormalizeConfigStringSlice(fallback); len(out) > 0 {
		return out
	}
	return nil
}

func firstNonEmptyFlags(primary, fallback map[string]bool) map[string]bool {
	if len(primary) > 0 {
		return clonePrepareFlags(primary)
	}
	return clonePrepareFlags(fallback)
}

func clonePrepareFlags(flags map[string]bool) map[string]bool {
	if len(flags) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(flags))
	for key, value := range flags {
		key = strings.TrimSpace(key)
		if key != "" {
			cloned[key] = value
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneMCPSnapshot(snapshot contract.MCPSnapshot) contract.MCPSnapshot {
	return mergeMCPSnapshot(snapshot, contract.MCPSnapshot{})
}

func mergeMCPSnapshot(base, extra contract.MCPSnapshot) contract.MCPSnapshot {
	out := contract.MCPSnapshot{
		Servers: providershared.NormalizeConfigStringSlice(append(append([]string(nil), base.Servers...), extra.Servers...)),
		Tools:   providershared.NormalizeConfigStringSlice(append(append([]string(nil), base.Tools...), extra.Tools...)),
	}
	if len(base.Instructions) > 0 || len(extra.Instructions) > 0 {
		out.Instructions = make(map[string]string, len(base.Instructions)+len(extra.Instructions))
		for key, value := range extra.Instructions {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				out.Instructions[key] = value
			}
		}
		for key, value := range base.Instructions {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				out.Instructions[key] = value
			}
		}
		if len(out.Instructions) == 0 {
			out.Instructions = nil
		}
	}
	return out
}

func readThreadRuntimeConfig(ctx context.Context, reader ThreadStateConfigReader, threadID string) map[string]any {
	if reader == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	cfg, err := reader.ReadThreadStateRuntimeConfig(ctx, threadID)
	if err != nil {
		return nil
	}
	return platformshared.CloneRuntimeConfigMap(cfg)
}

func requireTurnContext(
	ctx context.Context,
	session contract.Session,
	requestedThreadID ...string,
) (context.Context, string, error) {
	ctx = platformshared.NonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return ctx, "", err
	}
	if session == nil {
		return ctx, "", errors.New("session is required")
	}
	threadID := ""
	if len(requestedThreadID) > 0 {
		threadID = requestedThreadID[0]
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = strings.TrimSpace(session.ThreadID())
	}
	if threadID == "" {
		return ctx, "", errors.New("thread id is required")
	}
	return ctx, threadID, nil
}

func interruptAndWait(
	ctx context.Context,
	session contract.Session,
	tracker *turnTracker,
	active activeTurn,
	threadID string,
	source string,
	wait func() error,
) (bool, error) {
	if err := session.Interrupt(ctx, dto.InterruptRequest{
		ThreadID: threadID,
		Source:   strings.TrimSpace(source),
	}); err != nil {
		return false, err
	}
	if tracker == nil || !tracker.MarkInterruptRequested(active.localID) {
		return false, nil
	}
	if wait == nil {
		return true, nil
	}
	return true, wait()
}

func buildInterruptResult(status TurnStatus, envelope turnInterruptEnvelope) turnInterruptResult {
	result := turnInterruptResult{OK: true, TurnID: status.LocalID, Status: status.State}
	if envelope.mode == "" {
		envelope = buildTurnInterruptEnvelope(status.State, status.State, false, false, 0, false)
	}
	result.Confirmed = envelope.confirmed
	result.Mode = envelope.mode
	result.InterruptSent = envelope.interruptSent
	result.StateBefore = envelope.stateBefore
	result.StateAfter = envelope.stateAfter
	if envelope.interruptSent {
		waitedMS := envelope.waitedMS
		activeObserved := envelope.activeObserved
		result.WaitedMS = &waitedMS
		result.ActiveObserved = &activeObserved
	}
	return result
}

func normalizeSkillNames(groups ...[]string) []dto.SkillRef {
	refGroups := make([][]dto.SkillRef, 0, len(groups))
	for _, names := range groups {
		refs := make([]dto.SkillRef, 0, len(names))
		for _, raw := range names {
			refs = append(refs, dto.SkillRef{Name: raw})
		}
		refGroups = append(refGroups, refs)
	}
	return normalizeSkillRefs(refGroups...)
}

func decodeLegacyTurnParams[T any, L any](
	data []byte,
	target *T,
	legacy *L,
	merge func(current *T, legacy *L) error,
) error {
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	if legacy == nil || merge == nil {
		return nil
	}
	if err := json.Unmarshal(data, legacy); err != nil {
		return err
	}
	return merge(target, legacy)
}

func newSteerRequest(req dto.TurnRequest, expectedTurnID string) dto.SteerRequest {
	return dto.SteerRequest{
		ThreadID:             req.ThreadID,
		ExpectedTurnID:       strings.TrimSpace(expectedTurnID),
		Inputs:               append([]dto.InputItem(nil), req.Inputs...),
		Skills:               cloneSkillRefs(req.Skills),
		TurnAssembly:         req.TurnAssembly,
		ManualSkillSelection: req.ManualSkillSelection,
		OutputSchema:         append([]byte(nil), req.OutputSchema...),
		Overrides:            req.Overrides,
	}
}

func cloneSkillRefs(refs []dto.SkillRef) []dto.SkillRef {
	if len(refs) == 0 {
		return nil
	}
	cloned := make([]dto.SkillRef, len(refs))
	copy(cloned, refs)
	return cloned
}
