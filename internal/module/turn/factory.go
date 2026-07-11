package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/configutil"
)

// prepareInputSpec 是 buildPrepareInput 的参数结构体，字段对应 PrepareInput 但不含运行时派生字段。
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

// prepareSkillSpec 描述 buildPrepareInput 的技能来源：显式选中、精确 ref 和自动推导。
type prepareSkillSpec struct {
	Selected     []string
	SelectedRefs []skillRefParams
	Derived      []string
}

// prepareInputSession 是 PrepareInput 构建阶段需要读取的最小 session 能力接口。
type prepareInputSession interface {
	Capabilities() dto.CapabilitySet
}

// runtimeConfigSnapshotReader 允许 session 暴露当前运行时配置快照，用于补齐 turn 输入。
type runtimeConfigSnapshotReader interface {
	RuntimeConfigSnapshot() map[string]any
}

// buildPrepareInput 从 RPC/队列输入、技能来源和 session 能力构建 provider turn 的准备输入。
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
		ThreadRuntimeConfig:          clone.RuntimeConfigMap(spec.ThreadRuntimeConfig),
		ThreadCaps:                   caps,
		BinaryDir:                    spec.BinaryDir,
	}
	input = hydratePrepareInput(input, session)
	input.EnabledTools = applyPersistentSubagentToolPolicy(input.EnabledTools, input.SessionFlags)
	return input
}

// hydratePrepareInput 从 ThreadRuntimeConfig 和 session 的 RuntimeConfigSnapshot 合并运行时配置到 input。
func hydratePrepareInput(input PrepareInput, session prepareInputSession) PrepareInput {
	input = mergePrepareInputRuntime(input, input.ThreadRuntimeConfig)
	reader, ok := session.(runtimeConfigSnapshotReader)
	if !ok {
		return input
	}
	return mergePrepareInputRuntime(input, reader.RuntimeConfigSnapshot())
}

// mergePrepareInputRuntime 把运行时配置 map 中的字段合并到 input，已有非空值的字段不被覆盖。
func mergePrepareInputRuntime(input PrepareInput, cfg map[string]any) PrepareInput {
	if len(cfg) == 0 {
		return input
	}
	input.Provider = util.FirstNonEmpty(strings.TrimSpace(input.Provider), configutil.ConfigString(cfg, contract.RuntimeConfigProvider.Keys()...))
	input.PromptKey = util.FirstNonEmpty(strings.TrimSpace(input.PromptKey), configutil.ConfigString(cfg, contract.RuntimeConfigPromptKey.Keys()...))
	input.CWD = util.FirstNonEmpty(strings.TrimSpace(input.CWD), configutil.ConfigString(cfg, contract.RuntimeConfigCWD.Keys()...))
	input.Model = util.FirstNonEmpty(strings.TrimSpace(input.Model), configutil.ConfigString(cfg, contract.RuntimeConfigModel.Keys()...))
	input.GitRoot = util.FirstNonEmpty(strings.TrimSpace(input.GitRoot), configutil.ConfigString(cfg, contract.RuntimeConfigGitRoot.Keys()...))
	input.IsWorktree = input.IsWorktree || configBool(cfg, contract.RuntimeConfigIsWorktree.Keys()...)
	input.Language = util.FirstNonEmpty(strings.TrimSpace(input.Language), configutil.ConfigString(cfg, contract.RuntimeConfigLanguage.Keys()...))
	input.EnabledTools = firstNonEmptyStrings(input.EnabledTools, configutil.ConfigStringSlice(cfg, contract.RuntimeConfigEnabledTools.Keys()...))
	input.AdditionalWorkingDirectories = firstNonEmptyStrings(input.AdditionalWorkingDirectories, configutil.ConfigStringSlice(cfg, contract.RuntimeConfigAdditionalWorkingDirectories.Keys()...))
	input.MCPSnapshot = mergeMCPSnapshot(input.MCPSnapshot, configMCPSnapshot(cfg))
	input.SessionFlags = firstNonEmptyFlags(input.SessionFlags, configBoolMap(cfg, contract.RuntimeConfigSessionFlags.Keys()...))
	input.Summary = util.FirstNonEmpty(strings.TrimSpace(input.Summary), configutil.ConfigString(cfg, contract.RuntimeConfigSummary.Keys()...))
	input.OutputStyleConfig = firstNonNilOutputStyle(input.OutputStyleConfig, configOutputStyle(cfg, contract.RuntimeConfigOutputStyleConfig.Keys()...))
	input.ScratchpadDir = util.FirstNonEmpty(strings.TrimSpace(input.ScratchpadDir), configScratchpadDir(cfg, contract.RuntimeConfigScratchpadDir.Keys()...))
	if input.FRCConfig == nil {
		input.FRCConfig = configFRCConfig(cfg, contract.RuntimeConfigFRCConfig.Keys()...)
	}
	if providerNativeSkillsDisabled(cfg) {
		input.ManualSkillSelection = true
	}
	return input
}

// providerNativeSkillsDisabled 读取新旧配置键，判断 provider 原生 skill 是否被显式禁用。
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

// configBool 从动态配置中读取第一个 bool 值，非 bool 旧值不会被隐式转换。
func configBool(cfg map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := cfg[key].(bool); ok {
			return value
		}
	}
	return false
}

// configBoolMap 读取 session flags 一类 bool map，兼容 map[string]bool 与 JSON map。
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

// normalizePrepareBoolMap 清洗动态 bool map，忽略非 bool 值和空 key。
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

// configMCPSnapshot 从运行时配置中抽取 MCP server、tool 和指令增量快照。
func configMCPSnapshot(cfg map[string]any) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:                  configutil.ConfigStringSlice(cfg, contract.RuntimeConfigMCPServers.Keys()...),
		Tools:                    configutil.ConfigStringSlice(cfg, contract.RuntimeConfigMCPTools.Keys()...),
		Instructions:             configStringMap(cfg, contract.RuntimeConfigMCPInstructions.Keys()...),
		InstructionsDeltaEnabled: configBool(cfg, contract.RuntimeConfigMCPInstructionsDeltaEnabled.Keys()...),
	}
}

// configStringMap 从动态配置中读取字符串 map，并交给 configutil 做类型兼容。
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

// firstNonEmptyStrings 返回首个非空字符串切片，并在返回前执行标准化。
func firstNonEmptyStrings(primary, fallback []string) []string {
	if out := configutil.NormalizeConfigStringSlice(primary); len(out) > 0 {
		return out
	}
	if out := configutil.NormalizeConfigStringSlice(fallback); len(out) > 0 {
		return out
	}
	return nil
}

// firstNonEmptyFlags 返回调用方已有 flags 或 fallback 的深拷贝，避免后续合并写穿来源 map。
func firstNonEmptyFlags(primary, fallback map[string]bool) map[string]bool {
	if len(primary) > 0 {
		return clonePrepareFlags(primary)
	}
	return clonePrepareFlags(fallback)
}

// clonePrepareFlags 复制并清理 session flag key，空 key 不进入 provider 输入。
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

// applyPersistentSubagentToolPolicy 在持久子代理默认开启时移除旧 spawn_agent，避免两套入口并存。
func applyPersistentSubagentToolPolicy(enabledTools []string, flags map[string]bool) []string {
	if !persistentSubagentDefaultEnabled(flags) || len(enabledTools) == 0 {
		return enabledTools
	}
	hasManaged := false
	hasSpawn := false
	for _, tool := range enabledTools {
		managed, spawn := subagentToolPolicyFlags(tool)
		hasManaged = hasManaged || managed
		hasSpawn = hasSpawn || spawn
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

func subagentToolPolicyFlags(tool string) (managed, spawn bool) {
	switch strings.TrimSpace(tool) {
	case "spawn_agent":
		return false, true
	default:
		return contract.IsOrchestrationLaunchTool(tool), false
	}
}

// persistentSubagentDefaultEnabled 兼容多个历史 flag 名，判断 UI 是否要求托管子代理入口优先。
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

// cloneMCPSnapshot 深拷贝 MCP 快照，调用方可安全继续修改自己的输入。
func cloneMCPSnapshot(snapshot contract.MCPSnapshot) contract.MCPSnapshot {
	return mergeMCPSnapshot(snapshot, contract.MCPSnapshot{})
}

// mergeMCPSnapshot 合并基础快照和运行时补充，server 配置名也会并入 server 列表。
func mergeMCPSnapshot(base, extra contract.MCPSnapshot) contract.MCPSnapshot {
	serverConfigs := mergeTurnMCPServerConfigMaps(base.ServerConfigs, extra.ServerConfigs)
	return contract.MCPSnapshot{
		Servers:                  uniqueTurnMCPServerNames(base.Servers, extra.Servers, turnMCPServerConfigNames(serverConfigs)),
		Tools:                    configutil.NormalizeConfigStringSlice(append(append([]string(nil), base.Tools...), extra.Tools...)),
		Instructions:             mergeMCPInstructions(base.Instructions, extra.Instructions),
		ServerConfigs:            serverConfigs,
		InstructionsDeltaEnabled: base.InstructionsDeltaEnabled || extra.InstructionsDeltaEnabled,
		InstructionAttachments:   append(append([]contract.MCPAttachmentRef(nil), base.InstructionAttachments...), extra.InstructionAttachments...),
	}
}

// mergeMCPInstructions 合并 MCP 指令，base 优先覆盖 extra，返回前清理空 key/value。
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

// appendMCPInstructions 把非空 MCP 指令写入目标 map，调用方负责合并优先级。
func appendMCPInstructions(dst, src map[string]string) {
	for key, value := range src {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			dst[key] = value
		}
	}
}

// readThreadRuntimeConfig 从 ThreadStateConfigReader 读取线程运行时配置并深拷贝返回，reader 或 threadID 为空时返回 nil。
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
	return clone.RuntimeConfigMap(cfg), nil
}

// requireTurnContext 校验 session 和 threadID，并兼容 RPC middleware 注入的线程上下文。
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
	// provider 真实 UUID 可能在首轮 turn 后才异步返回；此时回退到 RPC ThreadScope
	// 注入的线程 ID，避免准备阶段因为 session.ThreadID 尚未解析而失败。
	if threadID == "" {
		threadID = strings.TrimSpace(contract.ThreadIDFrom(ctx))
	}
	if threadID == "" {
		return ctx, "", errors.New("thread id is required")
	}
	return ctx, threadID, nil
}

// interruptAndWait 先把中断请求发给 provider，再把本地 tracker 推进到 interrupting。
// wait 由调用方决定是否等待 handle 收敛；返回值只表示 provider 中断请求是否已成功发出。
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

// activeProviderID 从 activeTurn 的 handle 或缓存字段中取 provider turn ID，优先使用 handle 的实时值。
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

// buildInterruptResult 将 TurnStatus 和 turnInterruptEnvelope 组装为 RPC 层的 turnInterruptResult。
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

// buildInterruptFailureResult 构造 ok=false 的中断结果，用于中断失败或超时路径。
func buildInterruptFailureResult(status TurnStatus, envelope turnInterruptEnvelope) turnInterruptResult {
	result := buildInterruptResult(status, envelope)
	result.OK = false
	return result
}

// normalizePrepareSkillRefs 合并 selected、selectedRefs 和 derived 三路技能来源，去重后返回最终 SkillRef 列表。
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

// dropNameOnlySkillRefsCoveredByExactRefs 移除已被精确 ref 覆盖的 name-only skill，保留作用域信息。
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

// exactSkillRefNameKeys 返回带 key/scope/path 等精确身份信息的 skill 名称集合。
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

// hasExactSkillRefIdentity 判断 SkillRef 是否携带足以区分同名 skill 的身份字段。
func hasExactSkillRefIdentity(ref dto.SkillRef) bool {
	return strings.TrimSpace(ref.Key) != "" ||
		strings.TrimSpace(ref.Scope) != "" ||
		strings.TrimSpace(ref.PersonalType) != "" ||
		strings.TrimSpace(ref.Path) != ""
}

// normalizeSkillRefsWithSource 把 RPC skillRef 参数转为 provider SkillRef，并保留显式 source。
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

// normalizeSkillNames 把多组 name-only skill 合并为去重后的 SkillRef 列表。
func normalizeSkillNames(groups ...[]string) []dto.SkillRef {
	refGroups := make([][]dto.SkillRef, 0, len(groups))
	for _, names := range groups {
		refGroups = append(refGroups, normalizeSkillNamesWithSource(dto.SkillSourceUnspecified, names))
	}
	return normalizeSkillRefs(refGroups...)
}

// normalizeSkillNamesWithSource 给 name-only skill 标记来源，供后续 hydration 判断可信度。
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

// decodeLegacyTurnParams 先解码当前 wire 结构，再读取兼容字段并合并到 target。
// merge 负责决定哪些旧字段仍允许回填，避免兼容输入静默覆盖当前参数。
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

// newSteerRequest 根据已准备好的 TurnRequest 和当前 provider turn ID 构造 SteerRequest。
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

// cloneSkillRefs 复制 SkillRef 切片，避免 PrepareInput 与请求参数共享底层数组。
func cloneSkillRefs(refs []dto.SkillRef) []dto.SkillRef {
	if len(refs) == 0 {
		return nil
	}
	cloned := make([]dto.SkillRef, len(refs))
	copy(cloned, refs)
	return cloned
}
