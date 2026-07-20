package toolbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
)

type codexDisabledToolSet map[string]struct{}

func newCodexDisabledToolSet(values []string) codexDisabledToolSet {
	if len(values) == 0 {
		return nil
	}
	out := make(codexDisabledToolSet, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s codexDisabledToolSet) match(names ...string) (string, bool) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := s[name]; ok {
			return name, true
		}
	}
	return "", false
}

func mcpSurfaceToolAliases(family, realName, canonical string) []string {
	return []string{canonical, realName, wrappedMCPToolName(family, realName)}
}

// addSingleMCPToolToSurface 处理一个 MCP 工具的可见性、禁用别名和可调用入口。
// 被禁用的工具只登记 deny 别名，不会写入 surface.tools 或返回给 Codex 的 schema。
func addSingleMCPToolToSurface(
	surface *codexToolSurface,
	out *[]contract.DynamicToolSchema,
	family string,
	client mcpClient,
	admitted admittedMCPTool,
	disabled codexDisabledToolSet,
) error {
	tool := admitted.tool
	if _, reserved := reservedHostOnlySurfaceToolCanonicalName(family, tool.Name); reserved {
		return nil
	}
	if err := validateCurrentLSPPeerToolName(family, tool.Name); err != nil {
		return err
	}
	canonical := canonicalCodexToolName(family, tool.Name)
	if shouldNamespaceExternalMCPTool(surface, family, canonical) {
		canonical = wrappedMCPToolName(family, tool.Name)
	}
	aliases := mcpSurfaceToolAliases(family, tool.Name, canonical)
	if disabledName, ok := disabled.match(aliases...); ok {
		return addDisabledSurfaceToolAliases(surface, disabledName, aliases...)
	}
	entry := codexToolEntry{
		name: canonical, realName: tool.Name, executionKind: "stdio",
		family: strings.TrimSpace(family), client: client, taskSupport: admitted.taskSupport,
		compiledSchema: admitted.canonical, authority: admitted.authority,
	}
	if err := addSurfaceTool(surface, out, tool, entry); err != nil {
		return err
	}
	if err := addCallableMCPToolAliases(surface, family, tool.Name, canonical); err != nil {
		return err
	}
	return nil
}

func addCallableMCPToolAliases(surface *codexToolSurface, family, realName, canonical string) error {
	if err := addMCPToolAlias(surface, family, realName, canonical); err != nil {
		return err
	}
	return addSurfaceAlias(surface, wrappedMCPToolName(family, realName), canonical)
}

func validateCurrentLSPPeerToolName(family, name string) error {
	if strings.TrimSpace(family) != mcpdto.ClientKindLSP {
		return nil
	}
	if isCurrentLSPToolName(name) {
		return nil
	}
	return fmt.Errorf("toolbridge: LSP peer returned unsupported tool %q; expected current short-name contract", strings.TrimSpace(name))
}

// addDisabledSurfaceToolAliases 记录被 session config 禁用的工具别名。
// 禁用项不进入可调用表，但 scoped stale-call 会先命中这里并被拒绝。
func addDisabledSurfaceToolAliases(surface *codexToolSurface, disabledName string, aliases ...string) error {
	disabledName = strings.TrimSpace(disabledName)
	for _, alias := range nonEmptyUnique(aliases...) {
		if disabledName == "" {
			disabledName = alias
		}
		if existing, ok := surface.aliases[alias]; ok {
			return fmt.Errorf("toolbridge: disabled codex surface alias %q conflicts with visible tool %q", alias, existing)
		}
		if existing, ok := surface.disabledTools[alias]; ok && existing != disabledName {
			return fmt.Errorf("toolbridge: disabled codex surface alias %q maps to both %q and %q", alias, existing, disabledName)
		}
		surface.disabledTools[alias] = disabledName
	}
	return nil
}

func disabledCodexSurfaceToolCallResult(surface *codexToolSurface, name string) (*ToolCallResult, bool) {
	if surface == nil {
		return nil, false
	}
	disabledName, ok := surface.disabledTools[strings.TrimSpace(name)]
	if !ok {
		return nil, false
	}
	if disabledName = strings.TrimSpace(disabledName); disabledName == "" {
		disabledName = strings.TrimSpace(name)
	}
	return toolCallTextResult(false, fmt.Sprintf("codex surface tool %q is disabled by session config", disabledName)), true
}

// storeCodexToolSurface 将新的 Codex tool surface 写入索引。
// 如果同一 key 已绑定旧 surface，旧 surface 会在替换后关闭，确保 stdio client 不泄漏。
func (h *Handler) storeCodexToolSurface(surface *codexToolSurface) error {
	replaced := h.replaceCodexToolSurface(surface)
	var closeErrs []error
	for _, old := range replaced {
		if err := old.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("toolbridge: close replaced codex tool surface: %w", err))
		}
	}
	return errors.Join(closeErrs...)
}

// replaceCodexToolSurface 在锁内替换所有 surface key，并返回需要关闭的旧 surface。
func (h *Handler) replaceCodexToolSurface(surface *codexToolSurface) []*codexToolSurface {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	if h.surfaces == nil {
		h.surfaces = make(map[string]*codexToolSurface)
	}
	replaced := make([]*codexToolSurface, 0)
	seen := make(map[*codexToolSurface]struct{})
	for _, key := range surface.keys {
		old := h.surfaces[key]
		if old == nil || old == surface {
			continue
		}
		if _, ok := seen[old]; ok {
			continue
		}
		seen[old] = struct{}{}
		replaced = append(replaced, old)
	}
	for _, old := range replaced {
		for _, key := range old.keys {
			if h.surfaces[key] == old {
				delete(h.surfaces, key)
			}
		}
	}
	for _, key := range surface.keys {
		h.surfaces[key] = surface
	}
	return replaced
}

// BindCodexToolSurface 将已准备好的 Codex tool surface 绑定到更多 agent/thread key。
// sourceKey 必须已经存在，目标 key 不能被其他 surface 占用。
func (h *Handler) BindCodexToolSurface(scope contract.CodexToolSurfaceScope) error {
	sourceKey := firstNonEmptySurfaceKey(surfaceIDKey(scope.SurfaceID), scope.AgentID)
	if sourceKey == "" {
		return fmt.Errorf("toolbridge: codex tool surface bind source is required")
	}
	keys := codexSurfaceKeys(scope)
	if len(keys) < 2 {
		return fmt.Errorf("toolbridge: codex tool surface bind target key is required")
	}
	return h.bindCodexToolSurface(sourceKey, keys)
}

// bindCodexToolSurface 在锁内完成 key 绑定，并同步 surface.keys 供释放时反向清理。
func (h *Handler) bindCodexToolSurface(sourceKey string, keys []string) error {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	surface := h.surfaces[sourceKey]
	if surface == nil {
		return fmt.Errorf("toolbridge: codex tool surface is not prepared for agent %q", sourceKey)
	}
	for _, key := range keys {
		if existing := h.surfaces[key]; existing != nil && existing != surface {
			return fmt.Errorf("toolbridge: codex tool surface key %q is already bound", key)
		}
	}
	for _, key := range keys {
		h.surfaces[key] = surface
	}
	merged := append(append([]string(nil), surface.keys...), keys...)
	surface.keys = nonEmptyUnique(merged...)
	return nil
}

// firstNonEmptySurfaceKey 返回第一个非空 surface key，用于兼容 surfaceID 与 agentID 两种索引。
func firstNonEmptySurfaceKey(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// ReleaseCodexToolSurface 释放 scope 能命中的所有 Codex tool surface。
// 多个 key 指向同一 surface 时只关闭一次，关闭错误会合并返回。
func (h *Handler) ReleaseCodexToolSurface(scope contract.CodexToolSurfaceScope) error {
	keys := codexSurfaceKeys(scope)
	if len(keys) == 0 {
		return fmt.Errorf("toolbridge: codex tool surface release scope is empty")
	}
	surfaces := h.takeCodexToolSurfaces(keys)
	var err error
	for _, surface := range surfaces {
		err = errors.Join(err, surface.Close())
	}
	return err
}

// takeCodexToolSurfaces 在锁内摘除 keys 对应的唯一 surface 集合。
func (h *Handler) takeCodexToolSurfaces(keys []string) []*codexToolSurface {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	seen := make(map[*codexToolSurface]struct{})
	out := make([]*codexToolSurface, 0, len(keys))
	for _, key := range keys {
		surface := h.surfaces[key]
		if surface == nil {
			continue
		}
		if _, ok := seen[surface]; ok {
			continue
		}
		seen[surface] = struct{}{}
		out = append(out, surface)
	}
	for _, surface := range out {
		for _, key := range surface.keys {
			if h.surfaces[key] == surface {
				delete(h.surfaces, key)
			}
		}
	}
	return out
}

type mcpSchemaExecutor interface {
	Execute(context.Context, schema.Invocation, schema.FenceHook) (schema.Result, error)
}

type mcpSchemaAuthority struct {
	token       contract.MCPToolAuthority
	toolDigests map[string]string
	quarantine  map[string]string
}

type admittedMCPTool struct {
	tool        mcpdto.MCPTool
	canonical   schema.CanonicalSchema
	authority   *mcpSchemaAuthority
	taskSupport string
}

type lazyMCPSchemaExecutor struct {
	config           schema.ClientConfig
	profile          contract.DependencyProfile
	mu               sync.Mutex
	client           mcpSchemaExecutor
	terminalErr      error
	init             *mcpSchemaClientInit
	initializeClient func(context.Context) (mcpSchemaExecutor, error)
}

type mcpSchemaClientInit struct {
	done      chan struct{}
	client    mcpSchemaExecutor
	err       error
	retryable bool
}

// Execute 延迟绑定当前应用 identity，并只执行已校验后固定的 helper 镜像。
func (executor *lazyMCPSchemaExecutor) Execute(ctx context.Context, invocation schema.Invocation, fence schema.FenceHook) (schema.Result, error) {
	if ctx == nil {
		return schema.Result{}, fmt.Errorf("toolbridge: MCP schema context is required")
	}
	operationCtx, cancel := schema.WithOperationDeadline(ctx)
	defer cancel()
	client, err := executor.resolveClient(operationCtx)
	if err != nil {
		return schema.Result{}, err
	}
	return client.Execute(operationCtx, invocation, fence)
}

// resolveClient 合并并发初始化；取消与瞬态初始化失败允许下一轮重新竞争。
func (executor *lazyMCPSchemaExecutor) resolveClient(ctx context.Context) (mcpSchemaExecutor, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("toolbridge: MCP schema initialization context: %w", err)
		}
		state, owner, client, err := executor.initializationState()
		if client != nil || err != nil {
			return client, err
		}
		if owner {
			executor.runInitialization(ctx, state)
			return state.client, state.err
		}
		select {
		case <-state.done:
			if state.retryable {
				continue
			}
			return state.client, state.err
		case <-ctx.Done():
			return nil, fmt.Errorf("toolbridge: wait for MCP schema client initialization: %w", ctx.Err())
		}
	}
}

func (executor *lazyMCPSchemaExecutor) initializationState() (*mcpSchemaClientInit, bool, mcpSchemaExecutor, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.client != nil || executor.terminalErr != nil {
		return nil, false, executor.client, executor.terminalErr
	}
	if executor.init != nil {
		return executor.init, false, nil, nil
	}
	state := &mcpSchemaClientInit{done: make(chan struct{})}
	executor.init = state
	return state, true, nil, nil
}

func (executor *lazyMCPSchemaExecutor) runInitialization(ctx context.Context, state *mcpSchemaClientInit) {
	client, err := executor.initialize(ctx)
	if client == nil && err == nil {
		err = schema.StableInitializationError(
			errors.New("toolbridge: MCP schema initializer returned nil client"),
		)
	}
	retryable := initializationFailureRetryable(ctx, err)
	executor.mu.Lock()
	state.client, state.err, state.retryable = client, err, retryable
	if err == nil {
		executor.client = client
	} else if !retryable {
		executor.terminalErr = err
	}
	executor.init = nil
	close(state.done)
	executor.mu.Unlock()
}

func initializationFailureRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return true
	}
	class, ok := schema.InitializationFailureClassOf(err)
	return !ok || class == schema.InitializationFailureTransient
}

func (executor *lazyMCPSchemaExecutor) initialize(ctx context.Context) (mcpSchemaExecutor, error) {
	if executor.initializeClient != nil {
		return executor.initializeClient(ctx)
	}
	identity, err := runningSchemaHelperIdentity()
	if err != nil {
		return nil, schema.StableInitializationError(err)
	}
	executor.config.Identity = identity
	workerPath, err := schemaFilesystemWorkerPath(executor.profile)
	if err != nil {
		return nil, err
	}
	executor.config.FilesystemWorkerPath = workerPath
	return schema.NewClient(ctx, executor.config)
}

func newMCPSchemaExecutor(cfgProjectRoot string, profile contract.DependencyProfile) (mcpSchemaExecutor, error) {
	helperDir, err := schemaHelperDirectory(cfgProjectRoot, profile)
	if err != nil {
		return nil, err
	}
	helperName := schema.HelperFileName(runtime.GOOS)
	return &lazyMCPSchemaExecutor{profile: profile, config: schema.ClientConfig{
		HelperPath:   filepath.Join(helperDir, helperName),
		ManifestPath: filepath.Join(helperDir, schema.HelperManifestFileName(runtime.GOOS)),
	}}, nil
}

func schemaFilesystemWorkerPath(profile contract.DependencyProfile) (string, error) {
	path, err := schema.PreparedFilesystemWorkerPath()
	if err == nil {
		return path, nil
	}
	if profile == contract.DependencyProfileProduction {
		return "", schema.StableInitializationError(fmt.Errorf("toolbridge: %w", err))
	}
	path, err = os.Executable()
	if err != nil {
		return "", schema.TransientInitializationError(
			fmt.Errorf("toolbridge: resolve schema filesystem worker: %w", err),
		)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", schema.TransientInitializationError(
			fmt.Errorf("toolbridge: canonicalize schema filesystem worker: %w", err),
		)
	}
	return filepath.Clean(path), nil
}

func schemaHelperDirectory(cfgProjectRoot string, profile contract.DependencyProfile) (string, error) {

	switch profile {
	case contract.DependencyProfileProduction:
		return packagedSchemaHelperDirectory()
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileTest:
		return developmentSchemaHelperDirectory(cfgProjectRoot)
	default:
		return "", fmt.Errorf("toolbridge: explicit dependency profile is required for schema helper")
	}
}

// packagedSchemaHelperDirectory 仅从当前可执行文件推导 canonical package 目录。
func packagedSchemaHelperDirectory() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("toolbridge: resolve executable for schema helper: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("toolbridge: canonicalize executable for schema helper: %w", err)
	}
	dir := filepath.Dir(executable)
	if runtime.GOOS == "darwin" && filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		return filepath.Join(filepath.Dir(dir), "Resources", "bin"), nil
	}
	return dir, nil
}

func developmentSchemaHelperDirectory(cfgProjectRoot string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(cfgProjectRoot))
	if root == "." || !filepath.IsAbs(root) {
		return "", fmt.Errorf("toolbridge: absolute project root is required in schema helper development mode")
	}
	return filepath.Join(root, "bin"), nil
}

func runningSchemaHelperIdentity() (schema.HelperIdentity, error) {
	identity, err := schema.CurrentBuildIdentity()
	if err != nil {
		return schema.HelperIdentity{}, fmt.Errorf("toolbridge: %w", err)
	}
	return identity, nil
}

// admitMCPServerTools 为一个 current server generation 执行逐工具 schema admission。
func (h *Handler) admitMCPServerTools(
	ctx context.Context,
	cwd string,
	binary providerdto.MCPBinary,
	tools []mcpdto.MCPTool,
) ([]admittedMCPTool, *mcpSchemaAuthority, error) {
	authority, strictTools, err := h.beginMCPAuthority(ctx, cwd, binary, tools)
	if err != nil {
		return nil, nil, err
	}
	if h.schemaExecutor == nil {
		return nil, nil, fmt.Errorf("toolbridge: MCP schema executor is required")
	}
	admitted := make([]admittedMCPTool, 0, len(strictTools))
	quarantined := make(map[string]string)
	for _, tool := range strictTools {
		item, admissionErr := h.admitMCPTool(ctx, authority, tool)
		if admissionErr != nil {
			if err := handleMCPAdmissionError(authority, tool.Name, admissionErr, quarantined); err != nil {
				return nil, nil, err
			}
			continue
		}
		admitted = append(admitted, item)
	}
	if err := h.ensureMCPAuthorityCurrent(ctx, authority); err != nil {
		return nil, nil, err
	}
	authority.quarantine = quarantined
	return admitted, authority, nil
}

func (h *Handler) admitMCPTool(
	ctx context.Context,
	authority *mcpSchemaAuthority,
	tool mcpdto.MCPTool,
) (admittedMCPTool, error) {
	wire, err := decodeMCPToolWire(tool.RawJSON())
	if err != nil {
		return admittedMCPTool{}, err
	}
	canonical, err := schema.Canonicalize(tool.InputSchema)
	if err != nil {
		return admittedMCPTool{}, err
	}
	authority.toolDigests[tool.Name] = canonical.Digest
	if err := h.compileMCPToolSchema(ctx, authority, tool.Name, canonical); err != nil {
		return admittedMCPTool{}, err
	}
	tool.InputSchema = append(json.RawMessage(nil), canonical.Bytes...)
	return admittedMCPTool{
		tool: tool, canonical: canonical, authority: authority,
		taskSupport: wire.Execution.TaskSupport,
	}, nil
}

func handleMCPAdmissionError(
	authority *mcpSchemaAuthority,
	toolName string,
	err error,
	quarantined map[string]string,
) error {
	code := schema.ErrorCode(err)
	if authority.token.Managed || !mcpSchemaErrorQuarantinable(code) {
		return fmt.Errorf("toolbridge: MCP server %q tool %q schema admission: %w", authority.token.ServerID, toolName, schema.SafeRecoveryError(err))
	}
	quarantined[toolName] = string(code)
	return nil
}

func mcpSchemaErrorQuarantinable(code schema.Code) bool {
	switch code {
	case schema.CodeInvalidEnvelope, schema.CodeInputTooLarge, schema.CodeBudgetExceeded,
		schema.CodeExternalRefForbidden, schema.CodeDraftUnsupported, schema.CodeRootNotObject,
		schema.CodeCompileFailed:
		return true
	default:
		return false
	}
}

func (h *Handler) compileMCPToolSchema(
	ctx context.Context,
	authority *mcpSchemaAuthority,
	toolName string,
	canonical schema.CanonicalSchema,
) error {
	_, err := h.schemaExecutor.Execute(ctx, schema.Invocation{
		Operation:           schema.OperationCompile,
		RequestID:           mcpSchemaRequestID(authority, toolName, "compile"),
		ServerID:            authority.token.ServerID,
		ToolName:            toolName,
		AuthorityGeneration: authority.token.Generation,
		Schema:              canonical,
	}, h.mcpAuthorityFence(authority))
	return err
}

func (h *Handler) validateMCPToolCall(
	ctx context.Context,
	entry codexToolEntry,
	arguments json.RawMessage,
) error {
	if err := h.ensureMCPAuthorityCurrent(ctx, entry.authority); err != nil {
		return err
	}
	if h.schemaExecutor == nil {
		return fmt.Errorf("toolbridge: MCP schema executor is required")
	}
	result, err := h.schemaExecutor.Execute(ctx, schema.Invocation{
		Operation:           schema.OperationValidate,
		RequestID:           mcpSchemaRequestID(entry.authority, entry.realName, "validate"),
		ServerID:            entry.authority.token.ServerID,
		ToolName:            entry.realName,
		AuthorityGeneration: entry.authority.token.Generation,
		Schema:              entry.compiledSchema,
		Arguments:           arguments,
	}, h.mcpAuthorityFence(entry.authority))
	if err != nil {
		return schema.SafeRecoveryError(err)
	}
	if !result.ArgumentsValid {
		return fmt.Errorf("toolbridge: MCP tool %q arguments rejected by schema helper", entry.realName)
	}
	return nil
}

func mcpSchemaRequestID(authority *mcpSchemaAuthority, toolName, operation string) string {
	return fmt.Sprintf("%s/%d/%s/%s", authority.token.ServerID, authority.token.Generation, operation, toolName)
}

// beginMCPAuthority 严格解析 raw identity，并向 config owner 申请 generation。
func (h *Handler) beginMCPAuthority(
	ctx context.Context,
	cwd string,
	binary providerdto.MCPBinary,
	tools []mcpdto.MCPTool,
) (*mcpSchemaAuthority, []mcpdto.MCPTool, error) {
	strictTools, toolDigests, membershipDigest, err := mcpToolMembership(tools)
	if err != nil {
		return nil, nil, fmt.Errorf("toolbridge: MCP server %q identity: %w", binary.Name, err)
	}
	if h.authorityOwner == nil {
		return nil, nil, fmt.Errorf("toolbridge: MCP authority owner is required")
	}
	token, err := h.authorityOwner.IssueMCPToolAuthority(ctx, contract.MCPToolAuthorityIssueRequest{
		CWD: normalizeToolCallCWD(cwd), Binary: binary, MembershipDigest: membershipDigest,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("toolbridge: issue MCP authority: %w", err)
	}
	return &mcpSchemaAuthority{token: token, toolDigests: toolDigests}, strictTools, nil
}

// mcpToolMembership 直接解析每个 raw object，拒绝 duplicate/unknown/type-conflict identity。
func mcpToolMembership(tools []mcpdto.MCPTool) ([]mcpdto.MCPTool, map[string]string, string, error) {
	strictTools := make([]mcpdto.MCPTool, 0, len(tools))
	digests := make(map[string]string, len(tools))
	identityDigests := make(map[string]string, len(tools))
	for _, tool := range tools {
		strictTool, wire, err := decodeStrictRawMCPTool(tool.RawJSON())
		if err != nil {
			return nil, nil, "", err
		}
		if _, exists := digests[strictTool.Name]; exists {
			return nil, nil, "", fmt.Errorf("duplicate tool name %q", strictTool.Name)
		}
		sum := sha256.Sum256(strictTool.InputSchema)
		digests[strictTool.Name] = hex.EncodeToString(sum[:])
		identityHash := sha256.New()
		_, _ = identityHash.Write(strictTool.InputSchema)
		_, _ = identityHash.Write([]byte("\x00execution.taskSupport=" + wire.Execution.TaskSupport))
		identityDigests[strictTool.Name] = hex.EncodeToString(identityHash.Sum(nil))
		strictTools = append(strictTools, strictTool)
	}
	names := make([]string, 0, len(digests))
	for name := range digests {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = hash.Write([]byte(name + "\x00" + identityDigests[name] + "\n"))
	}
	return strictTools, digests, hex.EncodeToString(hash.Sum(nil)), nil
}

// decodeStrictRawMCPTool 从 raw object 建立唯一 identity，不信任 DTO 宽松投影。
func decodeStrictRawMCPTool(raw json.RawMessage) (mcpdto.MCPTool, mcpToolWire, error) {
	if _, err := decodeRawMCPToolFields(raw); err != nil {
		return mcpdto.MCPTool{}, mcpToolWire{}, err
	}
	wire, err := decodeMCPToolWire(raw)
	if err != nil {
		return mcpdto.MCPTool{}, mcpToolWire{}, err
	}
	tool := mcpdto.NewRawTool(raw)
	tool.Name = wire.Name
	if wire.Description != nil {
		tool.Description = *wire.Description
	}
	tool.InputSchema = append(json.RawMessage(nil), wire.InputSchema...)
	tool.OutputSchema = append(json.RawMessage(nil), wire.OutputSchema...)
	return tool, wire, nil
}

// decodeRawMCPToolFields 逐项读取 raw object，禁止未知键和重复键获权。
func decodeRawMCPToolFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("raw tool object is required")
	}
	allowed, err := mcpToolWireFieldSet()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode raw tool object: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("raw tool must be an object")
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		if err := decodeRawMCPToolField(decoder, fields, allowed); err != nil {
			return nil, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("close raw tool object: %w", err)
	}
	if err := ensureMCPToolJSONEOF(decoder); err != nil {
		return nil, err
	}
	return fields, nil
}

// decodeRawMCPToolField 校验并保存一个 raw tool 字段。
func decodeRawMCPToolField(
	decoder *json.Decoder,
	fields map[string]json.RawMessage,
	allowed map[string]struct{},
) error {
	keyToken, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode raw tool key: %w", err)
	}
	key, ok := keyToken.(string)
	if !ok {
		return fmt.Errorf("raw tool key must be a string")
	}
	if _, ok := allowed[key]; !ok {
		return fmt.Errorf("raw tool contains unknown field %q", key)
	}
	if _, duplicate := fields[key]; duplicate {
		return fmt.Errorf("raw tool contains duplicate field %q", key)
	}
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode raw tool field %q: %w", key, err)
	}
	fields[key] = append(json.RawMessage(nil), value...)
	return nil
}

func ensureMCPToolJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("raw tool contains trailing JSON")
		}
		return fmt.Errorf("decode raw tool trailing data: %w", err)
	}
	return nil
}

func (h *Handler) mcpAuthorityFence(authority *mcpSchemaAuthority) schema.FenceHook {
	return func(ctx context.Context, _ schema.FenceStage, identity schema.FenceIdentity) error {
		if identity.ServerID != authority.token.ServerID ||
			identity.AuthorityGeneration != authority.token.Generation ||
			authority.toolDigests[identity.ToolName] != identity.SchemaDigest {
			return fmt.Errorf("toolbridge: MCP schema identity is stale")
		}
		return h.ensureMCPAuthorityCurrent(ctx, authority)
	}
}

func (h *Handler) ensureMCPAuthorityCurrent(ctx context.Context, authority *mcpSchemaAuthority) error {
	if authority == nil {
		return fmt.Errorf("toolbridge: MCP authority is required")
	}
	if h.authorityOwner == nil {
		return fmt.Errorf("toolbridge: MCP authority owner is required")
	}
	return h.authorityOwner.CheckMCPToolAuthority(ctx, authority.token)
}

// publishMCPSurfaceCurrentCAS 在 config owner 的批量 current-CAS 内发布 surface 和 quarantine。
func (h *Handler) publishMCPSurfaceCurrentCAS(ctx context.Context, surface *codexToolSurface) error {
	if h.authorityOwner == nil || len(surface.authorities) == 0 {
		return fmt.Errorf("toolbridge: MCP authority owner and generations are required")
	}
	commits := make([]contract.MCPToolQuarantineCommit, 0, len(surface.authorities))
	for _, authority := range surface.authorities {
		commits = append(commits, contract.MCPToolQuarantineCommit{
			Authority: authority.token,
			Tools:     authority.quarantine,
		})
	}
	return h.authorityOwner.CompareAndSwapMCPToolQuarantines(ctx, commits, func() error {
		return h.storeCodexToolSurface(surface)
	})
}

// snapshotCodexToolSurfaceBindings 捕获准备开始时的 surface 指针，供失败清理做 pointer-CAS。
func (h *Handler) snapshotCodexToolSurfaceBindings(keys []string) map[string]*codexToolSurface {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	expected := make(map[string]*codexToolSurface, len(keys))
	for _, key := range keys {
		expected[key] = h.surfaces[key]
	}
	return expected
}

// revokeExpectedCodexToolSurface 只撤下仍指向准备快照或本地代际的 key，并关闭对应 clients。
func (h *Handler) revokeExpectedCodexToolSurface(expected *codexToolSurface) error {
	if h == nil || expected == nil {
		return nil
	}
	toClose := map[*codexToolSurface]struct{}{expected: {}}
	h.surfaceMu.Lock()
	for _, key := range expected.keys {
		current := h.surfaces[key]
		observed := expected.expected[key]
		if current == expected || observed != nil && current == observed {
			delete(h.surfaces, key)
			toClose[current] = struct{}{}
		}
	}
	h.surfaceMu.Unlock()
	var closeErr error
	for surface := range toClose {
		closeErr = errors.Join(closeErr, surface.Close())
	}
	return closeErr
}

func (h *Handler) ensureCodexSurfaceEntryCurrent(ctx context.Context, entry codexToolEntry) error {
	if entry.executionKind != "stdio" {
		return nil
	}
	return h.ensureMCPAuthorityCurrent(ctx, entry.authority)
}

func (h *Handler) validateCodexSurfaceEntryArguments(
	ctx context.Context,
	entry codexToolEntry,
	arguments json.RawMessage,
) error {
	if entry.executionKind == "stdio" {
		return h.validateMCPToolCall(ctx, entry, arguments)
	}
	return validateToolInputSchema(entry.name, entry.inputSchema, arguments)
}

func admittedMCPToolValues(admitted []admittedMCPTool) []mcpdto.MCPTool {
	tools := make([]mcpdto.MCPTool, 0, len(admitted))
	for _, item := range admitted {
		tools = append(tools, item.tool)
	}
	return tools
}

func filterAdmittedMCPTools(admitted []admittedMCPTool, visible []mcpdto.MCPTool) []admittedMCPTool {
	visibleNames := make(map[string]struct{}, len(visible))
	for _, tool := range visible {
		visibleNames[tool.Name] = struct{}{}
	}
	filtered := make([]admittedMCPTool, 0, len(visible))
	for _, item := range admitted {
		if _, ok := visibleNames[item.tool.Name]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
