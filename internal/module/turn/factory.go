package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
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
	Summary                      string
	OutputStyleConfig            *contract.OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *contract.FRCConfig
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
		Skills:                       normalizePrepareSkillRefs(skills, spec.ManualSkillSelection),
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
		Summary:                      strings.TrimSpace(spec.Summary),
		OutputStyleConfig:            cloneOutputStyleConfigValue(spec.OutputStyleConfig),
		ScratchpadDir:                strings.TrimSpace(spec.ScratchpadDir),
		FRCConfig:                    configFRCConfig(map[string]any{"frc": spec.FRCConfig}, "frc"),
		ThreadRuntimeConfig:          clone.RuntimeConfigMap(spec.ThreadRuntimeConfig),
		ThreadCaps:                   caps,
		BinaryDir:                    spec.BinaryDir,
	}
	input = hydratePrepareInput(input, session)
	input.EnabledTools = applyPersistentSubagentToolPolicy(input.EnabledTools, input.SessionFlags)
	return input
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
	input.Provider = util.FirstNonEmpty(strings.TrimSpace(input.Provider), configutil.ConfigString(cfg, "provider"))
	input.CWD = util.FirstNonEmpty(strings.TrimSpace(input.CWD), configutil.ConfigString(cfg, "cwd"))
	input.Model = util.FirstNonEmpty(strings.TrimSpace(input.Model), configutil.ConfigString(cfg, "model"))
	input.GitRoot = util.FirstNonEmpty(strings.TrimSpace(input.GitRoot), configutil.ConfigString(cfg, "gitRoot", "git_root"))
	input.IsWorktree = input.IsWorktree || configBool(cfg, "isWorktree", "is_worktree")
	input.Language = util.FirstNonEmpty(strings.TrimSpace(input.Language), configutil.ConfigString(cfg, "language"))
	input.EnabledTools = firstNonEmptyStrings(input.EnabledTools, configutil.ConfigStringSlice(cfg, "enabledTools", "enabled_tools", "tools"))
	input.AdditionalWorkingDirectories = firstNonEmptyStrings(input.AdditionalWorkingDirectories, configutil.ConfigStringSlice(cfg, "additionalWorkingDirectories", "additional_working_directories"))
	input.MCPSnapshot = mergeMCPSnapshot(input.MCPSnapshot, configMCPSnapshot(cfg))
	input.SessionFlags = firstNonEmptyFlags(input.SessionFlags, configBoolMap(cfg, "sessionFlags", "session_flags"))
	input.Summary = util.FirstNonEmpty(strings.TrimSpace(input.Summary), configutil.ConfigString(cfg, "summary"))
	input.OutputStyleConfig = firstNonNilOutputStyle(input.OutputStyleConfig, configOutputStyle(cfg, "outputStyleConfig", "output_style_config"))
	input.ScratchpadDir = util.FirstNonEmpty(strings.TrimSpace(input.ScratchpadDir), configScratchpadDir(cfg, "scratchpadDir", "scratchpad_dir"))
	if input.FRCConfig == nil {
		input.FRCConfig = configFRCConfig(cfg, "frcConfig", "frc_config")
	}
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
		Servers:                  configutil.ConfigStringSlice(cfg, "mcpServers", "mcp_servers"),
		Tools:                    configutil.ConfigStringSlice(cfg, "mcpTools", "mcp_tools"),
		Instructions:             configStringMap(cfg, "mcpInstructions", "mcp_instructions"),
		InstructionsDeltaEnabled: configBool(cfg, "mcpInstructionsDeltaEnabled", "mcp_instructions_delta_enabled"),
	}
}

func configStringMap(cfg map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if out := configutil.StringMap(value); len(out) > 0 {
			return out
		}
	}
	return nil
}

func firstNonEmptyStrings(primary, fallback []string) []string {
	if out := configutil.NormalizeConfigStringSlice(primary); len(out) > 0 {
		return out
	}
	if out := configutil.NormalizeConfigStringSlice(fallback); len(out) > 0 {
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

func applyPersistentSubagentToolPolicy(enabledTools []string, flags map[string]bool) []string {
	if !persistentSubagentDefaultEnabled(flags) || len(enabledTools) == 0 {
		return enabledTools
	}
	hasManaged := false
	hasSpawn := false
	for _, tool := range enabledTools {
		switch strings.TrimSpace(tool) {
		case "orchestration_launch_agent":
			hasManaged = true
		case "spawn_agent":
			hasSpawn = true
		}
	}
	if !hasManaged || !hasSpawn {
		return enabledTools
	}
	filtered := make([]string, 0, len(enabledTools)-1)
	for _, tool := range enabledTools {
		if strings.TrimSpace(tool) == "spawn_agent" {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func persistentSubagentDefaultEnabled(flags map[string]bool) bool {
	if len(flags) == 0 {
		return false
	}
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	for key, enabled := range flags {
		if !enabled {
			continue
		}
		switch replacer.Replace(strings.ToLower(strings.TrimSpace(key))) {
		case "persistentsubagentdefault", "managedsubagentdefault", "uipersistentsubagentdefault":
			return true
		}
	}
	return false
}

func cloneMCPSnapshot(snapshot contract.MCPSnapshot) contract.MCPSnapshot {
	return mergeMCPSnapshot(snapshot, contract.MCPSnapshot{})
}

func mergeMCPSnapshot(base, extra contract.MCPSnapshot) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:                  configutil.NormalizeConfigStringSlice(append(append([]string(nil), base.Servers...), extra.Servers...)),
		Tools:                    configutil.NormalizeConfigStringSlice(append(append([]string(nil), base.Tools...), extra.Tools...)),
		Instructions:             mergeMCPInstructions(base.Instructions, extra.Instructions),
		InstructionsDeltaEnabled: base.InstructionsDeltaEnabled || extra.InstructionsDeltaEnabled,
		InstructionAttachments:   append(append([]contract.MCPAttachmentRef(nil), base.InstructionAttachments...), extra.InstructionAttachments...),
	}
}

func mergeMCPInstructions(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	appendMCPInstructions(out, extra)
	appendMCPInstructions(out, base)
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendMCPInstructions(dst, src map[string]string) {
	for key, value := range src {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			dst[key] = value
		}
	}
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
	return clone.RuntimeConfigMap(cfg)
}

func requireTurnContext(
	ctx context.Context,
	session contract.Session,
	requestedThreadID ...string,
) (context.Context, string, error) {
	ctx = util.NonNilContext(ctx)
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
		TurnID:   activeProviderID(active),
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

func activeProviderID(active activeTurn) string {
	if providerID := strings.TrimSpace(active.providerID); providerID != "" {
		return providerID
	}
	if active.handle == nil {
		return ""
	}
	return strings.TrimSpace(active.handle.ProviderID())
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

func normalizePrepareSkillRefs(skills prepareSkillSpec, manualSkillSelection bool) []dto.SkillRef {
	selectedSource := dto.SkillSourceUnspecified
	if manualSkillSelection {
		selectedSource = dto.SkillSourceManual
	}
	return normalizeSkillRefs(
		normalizeSkillNamesWithSource(selectedSource, skills.Selected),
		normalizeSkillNamesWithSource(dto.SkillSourceUnspecified, skills.Derived),
	)
}

func normalizeSkillNames(groups ...[]string) []dto.SkillRef {
	refGroups := make([][]dto.SkillRef, 0, len(groups))
	for _, names := range groups {
		refGroups = append(refGroups, normalizeSkillNamesWithSource(dto.SkillSourceUnspecified, names))
	}
	return normalizeSkillRefs(refGroups...)
}

func normalizeSkillNamesWithSource(source dto.SkillSource, names []string) []dto.SkillRef {
	refs := make([]dto.SkillRef, 0, len(names))
	for _, raw := range names {
		ref := dto.SkillRef{Name: raw}
		if source != dto.SkillSourceUnspecified {
			ref.Source = source
		}
		refs = append(refs, ref)
	}
	return refs
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
