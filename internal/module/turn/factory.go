package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
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
	PromptKey                    string
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
	Selected     []string
	SelectedRefs []skillRefParams
	Derived      []string
}

type prepareInputSession interface {
	Capabilities() dto.CapabilitySet
}

type runtimeConfigSnapshotReader interface {
	RuntimeConfigSnapshot() map[string]any
}

// buildPrepareInput 构建prepareinput。
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
		PromptKey:                    strings.TrimSpace(spec.PromptKey),
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
		ThreadRuntimeConfig:          kernel.CloneRuntimeConfigMap(spec.ThreadRuntimeConfig),
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
	input.Provider = kernel.FirstNonEmpty(strings.TrimSpace(input.Provider), kernel.ConfigString(cfg, contract.RuntimeConfigProvider.Keys()...))
	input.PromptKey = kernel.FirstNonEmpty(strings.TrimSpace(input.PromptKey), kernel.ConfigString(cfg, contract.RuntimeConfigPromptKey.Keys()...))
	input.CWD = kernel.FirstNonEmpty(strings.TrimSpace(input.CWD), kernel.ConfigString(cfg, contract.RuntimeConfigCWD.Keys()...))
	input.Model = kernel.FirstNonEmpty(strings.TrimSpace(input.Model), kernel.ConfigString(cfg, contract.RuntimeConfigModel.Keys()...))
	input.GitRoot = kernel.FirstNonEmpty(strings.TrimSpace(input.GitRoot), kernel.ConfigString(cfg, contract.RuntimeConfigGitRoot.Keys()...))
	input.IsWorktree = input.IsWorktree || configBool(cfg, contract.RuntimeConfigIsWorktree.Keys()...)
	input.Language = kernel.FirstNonEmpty(strings.TrimSpace(input.Language), kernel.ConfigString(cfg, contract.RuntimeConfigLanguage.Keys()...))
	input.EnabledTools = firstNonEmptyStrings(input.EnabledTools, kernel.ConfigStringSlice(cfg, contract.RuntimeConfigEnabledTools.Keys()...))
	input.AdditionalWorkingDirectories = firstNonEmptyStrings(input.AdditionalWorkingDirectories, kernel.ConfigStringSlice(cfg, contract.RuntimeConfigAdditionalWorkingDirectories.Keys()...))
	input.MCPSnapshot = mergeMCPSnapshot(input.MCPSnapshot, configMCPSnapshot(cfg))
	input.SessionFlags = firstNonEmptyFlags(input.SessionFlags, configBoolMap(cfg, contract.RuntimeConfigSessionFlags.Keys()...))
	input.Summary = kernel.FirstNonEmpty(strings.TrimSpace(input.Summary), kernel.ConfigString(cfg, contract.RuntimeConfigSummary.Keys()...))
	input.OutputStyleConfig = firstNonNilOutputStyle(input.OutputStyleConfig, configOutputStyle(cfg, contract.RuntimeConfigOutputStyleConfig.Keys()...))
	input.ScratchpadDir = kernel.FirstNonEmpty(strings.TrimSpace(input.ScratchpadDir), configScratchpadDir(cfg, contract.RuntimeConfigScratchpadDir.Keys()...))
	if input.FRCConfig == nil {
		input.FRCConfig = configFRCConfig(cfg, contract.RuntimeConfigFRCConfig.Keys()...)
	}
	if providerNativeSkillsDisabled(cfg) {
		input.ManualSkillSelection = true
	}
	return input
}

// providerNativeSkillsDisabled 处理providernativeskillsdisabled。
func providerNativeSkillsDisabled(cfg map[string]any) bool {
	for _, key := range contract.RuntimeConfigProviderNativeSkills.Keys() {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		enabled, ok := raw.(bool)
		return ok && !enabled
	}
	for _, key := range contract.RuntimeConfigDisableProviderNativeSkills.Keys() {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		disabled, ok := raw.(bool)
		return ok && disabled
	}
	return false
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

// normalizePrepareBoolMap 规范化prepareboolmap。
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
		Servers:                  kernel.ConfigStringSlice(cfg, contract.RuntimeConfigMCPServers.Keys()...),
		Tools:                    kernel.ConfigStringSlice(cfg, contract.RuntimeConfigMCPTools.Keys()...),
		Instructions:             configStringMap(cfg, contract.RuntimeConfigMCPInstructions.Keys()...),
		InstructionsDeltaEnabled: configBool(cfg, contract.RuntimeConfigMCPInstructionsDeltaEnabled.Keys()...),
	}
}

func configStringMap(cfg map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if out := kernel.ConfigStringMap(value); len(out) > 0 {
			return out
		}
	}
	return nil
}

func firstNonEmptyStrings(primary, fallback []string) []string {
	if out := kernel.NormalizeConfigStringSlice(primary); len(out) > 0 {
		return out
	}
	if out := kernel.NormalizeConfigStringSlice(fallback); len(out) > 0 {
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

// applyPersistentSubagentToolPolicy 应用persistentsubagent工具策略。
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
	serverConfigs := mergeTurnMCPServerConfigMaps(base.ServerConfigs, extra.ServerConfigs)
	return contract.MCPSnapshot{
		Servers:                  uniqueTurnMCPServerNames(base.Servers, extra.Servers, turnMCPServerConfigNames(serverConfigs)),
		Tools:                    kernel.NormalizeConfigStringSlice(append(append([]string(nil), base.Tools...), extra.Tools...)),
		Instructions:             mergeMCPInstructions(base.Instructions, extra.Instructions),
		ServerConfigs:            serverConfigs,
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

func readThreadRuntimeConfig(ctx context.Context, reader ThreadStateConfigReader, threadID string) (map[string]any, error) {
	if reader == nil {
		return nil, nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, nil
	}
	cfg, err := reader.ReadThreadStateRuntimeConfig(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return kernel.CloneRuntimeConfigMap(cfg), nil
}

// requireTurnContext 处理requireturn上下文。
func requireTurnContext(
	ctx context.Context,
	session contract.Session,
	requestedThreadID ...string,
) (context.Context, string, error) {
	ctx = kernel.NonNilContext(ctx)
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
	// Fallback to the thread ID injected into context by the RPC
	// ThreadScope middleware. This covers providers (e.g. Claude CLI)
	// whose session.ThreadID() has not yet been resolved because the
	// real provider UUID arrives asynchronously after the first turn.
	if threadID == "" {
		threadID = strings.TrimSpace(contract.ThreadIDFrom(ctx))
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
	if active.handle != nil {
		if providerID := strings.TrimSpace(active.handle.ProviderID()); providerID != "" {
			return providerID
		}
	}
	if providerID := strings.TrimSpace(active.providerID); providerID != "" {
		return providerID
	}
	return ""
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

func buildInterruptFailureResult(status TurnStatus, envelope turnInterruptEnvelope) turnInterruptResult {
	result := buildInterruptResult(status, envelope)
	result.OK = false
	return result
}

func normalizePrepareSkillRefs(skills prepareSkillSpec, manualSkillSelection bool) []dto.SkillRef {
	selectedSource := dto.SkillSourceUnspecified
	if manualSkillSelection {
		selectedSource = dto.SkillSourceManual
	}
	selectedRefs := normalizeSkillRefsWithSource(selectedSource, skills.SelectedRefs)
	return normalizeSkillRefs(
		selectedRefs,
		dropNameOnlySkillRefsCoveredByExactRefs(normalizeSkillNamesWithSource(selectedSource, skills.Selected), selectedRefs),
		dropNameOnlySkillRefsCoveredByExactRefs(normalizeSkillNamesWithSource(dto.SkillSourceUnspecified, skills.Derived), selectedRefs),
	)
}

// dropNameOnlySkillRefsCoveredByExactRefs 按精确refs去掉名称only技能refscovered。
func dropNameOnlySkillRefsCoveredByExactRefs(names []dto.SkillRef, refs []dto.SkillRef) []dto.SkillRef {
	if len(names) == 0 || len(refs) == 0 {
		return names
	}
	covered := exactSkillRefNameKeys(refs)
	if len(covered) == 0 {
		return names
	}
	filtered := make([]dto.SkillRef, 0, len(names))
	for _, ref := range names {
		nameKey := strings.ToLower(strings.TrimSpace(ref.Name))
		if nameKey == "" || covered[nameKey] {
			continue
		}
		filtered = append(filtered, ref)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func exactSkillRefNameKeys(refs []dto.SkillRef) map[string]bool {
	covered := make(map[string]bool, len(refs))
	for _, ref := range refs {
		nameKey := strings.ToLower(strings.TrimSpace(ref.Name))
		if nameKey == "" || !hasExactSkillRefIdentity(ref) {
			continue
		}
		covered[nameKey] = true
	}
	return covered
}

func hasExactSkillRefIdentity(ref dto.SkillRef) bool {
	return strings.TrimSpace(ref.Key) != "" ||
		strings.TrimSpace(ref.Scope) != "" ||
		strings.TrimSpace(ref.PersonalType) != "" ||
		strings.TrimSpace(ref.Path) != ""
}

func normalizeSkillRefsWithSource(source dto.SkillSource, refs []skillRefParams) []dto.SkillRef {
	out := make([]dto.SkillRef, 0, len(refs))
	for _, raw := range refs {
		refSource := source
		if rawSource := dto.SkillSource(strings.TrimSpace(raw.Source)); rawSource.Valid() && rawSource != dto.SkillSourceUnspecified {
			refSource = rawSource
		}
		ref := dto.SkillRef{
			Key:          raw.Key,
			Name:         raw.Name,
			Scope:        raw.Scope,
			PersonalType: raw.PersonalType,
			Path:         raw.Path,
		}
		if refSource != dto.SkillSourceUnspecified {
			ref.Source = refSource
		}
		out = append(out, ref)
	}
	return out
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
