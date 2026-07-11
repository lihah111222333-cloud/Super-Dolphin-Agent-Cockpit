package nested

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/pathutil"
)

// nestedGlobalThreadKey 承载缺少 threadID 时的全局 nested 状态。
const nestedGlobalThreadKey = "_global"

// NestedRuntime 跟踪每个 thread 已加载和待加载的 CLAUDE.md 来源。
// 所有会话状态都受 mu 保护，toolReadCacheRoot 在模块初始化后只读使用。
type NestedRuntime struct {
	deps Dependencies

	mu                sync.Mutex
	sessions          map[string]*nestedSessionState
	toolReadCacheRoot string // ToolCallEnd persistedPath 的安全读取根；空值表示禁用落盘结果读取。
}

// nestedSessionState 保存单个 thread 的 nested CLAUDE.md 注入进度。
type nestedSessionState struct {
	LoadedPaths     map[string]struct{} // 已注入来源，避免同一文件重复注入。
	PendingTriggers map[string]struct{} // 读工具或显式触发发现、等待消费的路径。
	Generation      uint64              // 每次 reset 递增，供调用方识别缓存失效。
	MatcherRoot     string              // GitRoot/CWD 组合，变化时重置路径匹配状态。
	BuildCtx        contract.BuildCtx   // 最近一次用于规范化触发路径的构建上下文。
}

// NewNestedRuntime 创建 nested CLAUDE.md 运行时，并延迟到首次 thread 事件再建状态。
func NewNestedRuntime(deps Dependencies) *NestedRuntime {
	return &NestedRuntime{
		deps:     deps,
		sessions: map[string]*nestedSessionState{},
	}
}

// SetToolReadCacheRoot 设置读取 ToolCallEnd persistedPath 时允许访问的缓存根。
// 空 root 会禁用落盘结果读取，调用方将回退到内存 preview。
// 这样可以避免信任工具事件里的任意路径。
func (r *NestedRuntime) SetToolReadCacheRoot(root string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolReadCacheRoot = strings.TrimSpace(root)
}

// OnThreadStart 为 thread 初始化独立 nested 状态，重复调用会清空旧的 pending/loaded 集合。
func (r *NestedRuntime) OnThreadStart(threadID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[nestedThreadKey(threadID)] = newNestedSessionState(1)
}

// OnPromptInvalidate 在 prompt、worktree 或 memory 写入失效时重置所有 nested 会话状态。
func (r *NestedRuntime) OnPromptInvalidate(reason contract.InvalidateReason) {
	if r == nil || !shouldResetNestedRuntime(reason) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, state := range r.sessions {
		resetNestedSessionState(state)
	}
}

// ObserveBuildContext 记录最新 BuildCtx，并在 GitRoot/CWD 变化时重置路径匹配缓存。
func (r *NestedRuntime) ObserveBuildContext(threadID string, buildCtx contract.BuildCtx) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateLocked(threadID)
	r.ensureMatcherRootLocked(state, buildCtx)
	state.BuildCtx = cloneNestedBuildCtx(buildCtx)
}

// AddTriggers 把候选文件路径规范化后加入 pending 集合。
// remote 输入和 memory 自身路径会被拒绝。
func (r *NestedRuntime) AddTriggers(threadID string, buildCtx contract.BuildCtx, triggers []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateLocked(threadID)
	r.ensureMatcherRootLocked(state, buildCtx)
	state.BuildCtx = cloneNestedBuildCtx(buildCtx)
	for _, trigger := range triggers {
		normalized, ok := r.normalizeTrigger(buildCtx, trigger)
		if ok {
			state.PendingTriggers[normalized] = struct{}{}
		}
	}
}

// ConsumePending 返回并清空当前 thread 的待加载 nested 来源。
func (r *NestedRuntime) ConsumePending(threadID string, buildCtx contract.BuildCtx) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateLocked(threadID)
	r.ensureMatcherRootLocked(state, buildCtx)
	state.BuildCtx = cloneNestedBuildCtx(buildCtx)
	pending := sortedNestedKeys(state.PendingTriggers)
	state.PendingTriggers = map[string]struct{}{}
	return pending
}

// MarkLoaded 标记来源已注入；返回 false 表示来源为空或本 thread 已加载过。
func (r *NestedRuntime) MarkLoaded(threadID string, buildCtx contract.BuildCtx, source ClaudeMdSource) bool {
	if r == nil {
		return false
	}
	key := nestedSourceKey(source)
	if strings.TrimSpace(key) == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateLocked(threadID)
	r.ensureMatcherRootLocked(state, buildCtx)
	state.BuildCtx = cloneNestedBuildCtx(buildCtx)
	if _, ok := state.LoadedPaths[key]; ok {
		return false
	}
	state.LoadedPaths[key] = struct{}{}
	return true
}

// AddToolReadResult 从 read 类工具输出中提取文件路径，并转成 nested pending trigger。
func (r *NestedRuntime) AddToolReadResult(threadID, toolName, result, persistedPath string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateLocked(threadID)
	if strings.TrimSpace(state.BuildCtx.CWD) == "" && strings.TrimSpace(state.BuildCtx.GitRoot) == "" {
		return
	}
	for _, trigger := range extractNestedReadToolPaths(r.toolReadCacheRoot, toolName, result, persistedPath) {
		normalized, ok := r.normalizeTrigger(state.BuildCtx, trigger)
		if ok {
			state.PendingTriggers[normalized] = struct{}{}
		}
	}
}

// snapshot 返回 thread 状态的防御性副本，供测试和诊断读取。
func (r *NestedRuntime) snapshot(threadID string) nestedSessionState {
	if r == nil {
		return nestedSessionState{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stateLocked(threadID)
	return nestedSessionState{
		LoadedPaths:     cloneNestedSet(state.LoadedPaths),
		PendingTriggers: cloneNestedSet(state.PendingTriggers),
		Generation:      state.Generation,
		MatcherRoot:     state.MatcherRoot,
	}
}

// stateLocked 返回 thread 状态；调用方必须持有 r.mu。
func (r *NestedRuntime) stateLocked(threadID string) *nestedSessionState {
	if r.sessions == nil {
		r.sessions = map[string]*nestedSessionState{}
	}
	key := nestedThreadKey(threadID)
	state, ok := r.sessions[key]
	if !ok {
		state = newNestedSessionState(1)
		r.sessions[key] = state
	}
	return state
}

// ensureMatcherRootLocked 在工作区根变化时重置会话状态。
// 旧路径不能在新 worktree 下继续匹配。
func (r *NestedRuntime) ensureMatcherRootLocked(state *nestedSessionState, buildCtx contract.BuildCtx) {
	root := nestedMatcherRoot(buildCtx)
	if state == nil || root == "" {
		return
	}
	if state.MatcherRoot == "" {
		state.MatcherRoot = root
		return
	}
	if state.MatcherRoot == root {
		return
	}
	state.MatcherRoot = root
	resetNestedSessionState(state)
}

// normalizeTrigger 将触发路径清洗为绝对路径，并拒绝 remote 输入和 memory 管理目录。
func (r *NestedRuntime) normalizeTrigger(buildCtx contract.BuildCtx, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || util.IsRemoteTurnInput(raw) {
		return "", false
	}
	if !filepath.IsAbs(raw) && strings.TrimSpace(buildCtx.CWD) != "" {
		raw = filepath.Join(strings.TrimSpace(buildCtx.CWD), raw)
	}
	path, err := shared.CleanAbsolutePath(raw)
	if err != nil || r.isDeniedTrigger(buildCtx, path) {
		return "", false
	}
	return path, true
}

// isDeniedTrigger 判断路径是否属于 memory 自身目录，避免 nested 反向读取自动记忆文件。
func (r *NestedRuntime) isDeniedTrigger(buildCtx contract.BuildCtx, path string) bool {
	switch {
	case shared.IsHistoricalAgentMemoryPath(path):
		return true
	case nestedContainsPath(r.deps.autoMemRoot(buildCtx), path):
		return true
	default:
		return nestedContainsPath(r.deps.teamRoot(buildCtx), path)
	}
}

// newNestedSessionState 初始化 thread 状态，generation 最小为 1 以便零值表示未初始化。
func newNestedSessionState(generation uint64) *nestedSessionState {
	if generation == 0 {
		generation = 1
	}
	return &nestedSessionState{
		LoadedPaths:     map[string]struct{}{},
		PendingTriggers: map[string]struct{}{},
		Generation:      generation,
	}
}

// resetNestedSessionState 清空已加载和待加载集合，并递增 generation 标记缓存失效。
func resetNestedSessionState(state *nestedSessionState) {
	if state == nil {
		return
	}
	state.Generation++
	if state.Generation == 0 {
		state.Generation = 1
	}
	state.LoadedPaths = map[string]struct{}{}
	state.PendingTriggers = map[string]struct{}{}
}

// shouldResetNestedRuntime 判断 prompt 失效原因是否会影响 CLAUDE.md 来源或 memory 可见性。
func shouldResetNestedRuntime(reason contract.InvalidateReason) bool {
	switch reason {
	case contract.InvalidateClear,
		contract.InvalidateCompact,
		contract.InvalidateWorktree,
		contract.InvalidateResumeRestore,
		contract.InvalidateMemoryWrite:
		return true
	default:
		return false
	}
}

// nestedThreadKey 将空 threadID 归入全局状态，避免 map key 为空字符串含义不清。
func nestedThreadKey(threadID string) string {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		return threadID
	}
	return nestedGlobalThreadKey
}

// nestedMatcherRoot 组合 GitRoot 和 CWD；任一变化都应让 nested 路径匹配重新开始。
func nestedMatcherRoot(buildCtx contract.BuildCtx) string {
	root := strings.TrimSpace(buildCtx.GitRoot)
	if root == "" {
		root = strings.TrimSpace(buildCtx.CWD)
	}
	root = cleanClaudeMdPath(root)
	cwd := cleanClaudeMdPath(buildCtx.CWD)
	if root == "" && cwd == "" {
		return ""
	}
	return root + "\n" + cwd
}

// nestedContainsPath 安全判断 child 是否位于 root 内，任一路径无法清洗时按不包含处理。
func nestedContainsPath(root, child string) bool {
	root = strings.TrimSpace(root)
	child = strings.TrimSpace(child)
	if root == "" || child == "" {
		return false
	}
	cleanRoot, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return false
	}
	cleanChild, err := shared.CleanAbsolutePath(child)
	if err != nil {
		return false
	}
	return pathutil.ContainsPath(cleanRoot, cleanChild)
}

// sortedNestedKeys 返回稳定顺序的集合内容，保证 prompt 注入顺序可复现。
func sortedNestedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

// cloneNestedSet 复制字符串集合，避免快照调用方共享内部 map。
func cloneNestedSet(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]struct{}, len(values))
	for value := range values {
		cloned[value] = struct{}{}
	}
	return cloned
}

// cloneNestedBuildCtx 复制 nested 需要的 BuildCtx 字段。
// 复制 slice 是为了避免后续调用方修改影响运行时状态。
func cloneNestedBuildCtx(buildCtx contract.BuildCtx) contract.BuildCtx {
	return contract.BuildCtx{
		CWD:                          strings.TrimSpace(buildCtx.CWD),
		GitRoot:                      strings.TrimSpace(buildCtx.GitRoot),
		AdditionalWorkingDirectories: append([]string(nil), buildCtx.AdditionalWorkingDirectories...),
		EnabledTools:                 append([]string(nil), buildCtx.EnabledTools...),
	}
}

// extractNestedReadToolPaths 从 read 工具输出中提取可触发 nested 注入的路径列表。
func extractNestedReadToolPaths(cacheRoot, toolName, preview, persistedPath string) []string {
	if !isNestedReadTool(toolName) {
		return nil
	}
	raw := nestedReadToolRaw(cacheRoot, preview, persistedPath)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return nestedReadToolContentPaths(raw)
}

// nestedReadToolRaw 优先读取受 cacheRoot 约束的持久化工具输出，失败时回退到事件 preview。
func nestedReadToolRaw(cacheRoot, preview, persistedPath string) string {
	if content, ok := readNestedPersistedToolOutput(cacheRoot, persistedPath); ok {
		return content
	}
	return strings.TrimSpace(preview)
}

// readNestedPersistedToolOutput 只通过 shared.SafeReadEntrypoint 读取持久化工具输出。
// cacheRoot 为空、路径非绝对或路径越界都会失败并回退 preview。
// forged persistedPath 不能借此读出工作区外文件。
func readNestedPersistedToolOutput(cacheRoot, persistedPath string) (string, bool) {
	root := strings.TrimSpace(cacheRoot)
	path := strings.TrimSpace(persistedPath)
	if root == "" || path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", false
	}
	content, _, err := shared.SafeReadEntrypoint(root, path)
	if err != nil {
		return "", false
	}
	raw := string(content)
	return raw, strings.TrimSpace(raw) != ""
}

// nestedReadToolContentPaths 扫描工具输出里的 “Contents of ...:” 行，并去重排序。
func nestedReadToolContentPaths(raw string) []string {
	paths := make(map[string]struct{}, 4)
	for line := range strings.SplitSeq(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if path := nestedReadToolContentPath(line); path != "" {
			paths[path] = struct{}{}
		}
	}
	return sortedNestedKeys(paths)
}

// nestedReadToolContentPath 从单行 read 工具输出中提取文件路径。
func nestedReadToolContentPath(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "Contents of ") || !strings.HasSuffix(trimmed, ":") {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "Contents of "), ":"))
}

// isNestedReadTool 判断工具名是否属于会暴露文件内容的 read 类工具。
func isNestedReadTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read", "fileread", "file_read", "readfile", "file_read_tool":
		return true
	default:
		return false
	}
}
