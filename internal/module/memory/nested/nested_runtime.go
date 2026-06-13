package nested

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

const nestedGlobalThreadKey = "_global"

type NestedRuntime struct {
	deps Dependencies

	mu                sync.Mutex
	sessions          map[string]*nestedSessionState
	toolReadCacheRoot string // P24 cache-root-threading: SafeReadEntrypoint root for ToolCallEnd persistedPath; empty disables persisted-output reads. Set once at module init via SetToolReadCacheRoot, then treated read-only under r.mu.
}

type nestedSessionState struct {
	LoadedPaths     map[string]struct{}
	PendingTriggers map[string]struct{}
	Generation      uint64
	MatcherRoot     string
	BuildCtx        contract.BuildCtx
}

// NewNestedRuntime 创建nested运行时。
func NewNestedRuntime(deps Dependencies) *NestedRuntime {
	return &NestedRuntime{
		deps:     deps,
		sessions: map[string]*nestedSessionState{},
	}
}

// SetToolReadCacheRoot threads the persisted tool-result cache root through to
// readNestedPersistedToolOutput so the slow-path os read is contained against a
// known root via shared.SafeReadEntrypoint instead of trusting the
// `persistedPath` field on ToolCallEnd to already be safe. Empty root disables
// persisted-output reads entirely (the helper falls back to the in-memory
// preview). See docs/plans/迁移/p24记忆优化/p24记忆优化.md.
func (r *NestedRuntime) SetToolReadCacheRoot(root string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolReadCacheRoot = strings.TrimSpace(root)
}

// OnThreadStart 处理on线程起点。
func (r *NestedRuntime) OnThreadStart(threadID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[nestedThreadKey(threadID)] = newNestedSessionState(1)
}

// OnPromptInvalidate 处理onpromptinvalidate。
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

// ObserveBuildContext 处理observebuild上下文。
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

// AddTriggers 添加triggers。
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

// ConsumePending 处理consume待处理。
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

// MarkLoaded 标记loaded。
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

// AddToolReadResult 添加工具read结果。
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

// normalizeTrigger 规范化trigger。
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

func nestedThreadKey(threadID string) string {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		return threadID
	}
	return nestedGlobalThreadKey
}

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

func cloneNestedBuildCtx(buildCtx contract.BuildCtx) contract.BuildCtx {
	return contract.BuildCtx{
		CWD:                          strings.TrimSpace(buildCtx.CWD),
		GitRoot:                      strings.TrimSpace(buildCtx.GitRoot),
		AdditionalWorkingDirectories: append([]string(nil), buildCtx.AdditionalWorkingDirectories...),
		EnabledTools:                 append([]string(nil), buildCtx.EnabledTools...),
	}
}

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

func nestedReadToolRaw(cacheRoot, preview, persistedPath string) string {
	if content, ok := readNestedPersistedToolOutput(cacheRoot, persistedPath); ok {
		return content
	}
	return strings.TrimSpace(preview)
}

// readNestedPersistedToolOutput reads a persisted ToolCallEnd payload via
// shared.SafeReadEntrypoint so the read is contained against the tool-result
// cache root threaded in by SetToolReadCacheRoot at module init. P22 P2
// Finding 10 still applies: this read runs on the nestedIngestWorker
// goroutine, not on the bus callback path. P24 cache-root-threading: an
// empty cacheRoot disables persisted-output reads outright (caller falls
// back to the in-memory preview); SafeReadEntrypoint also rejects any
// persistedPath that resolves outside cacheRoot, so a forged
// `/etc/passwd`-style PersistedPath on ToolCallEnd cannot trick the worker
// into reading from the CWD or above. See
// docs/plans/迁移/p24记忆优化/p24记忆优化.md.
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

func nestedReadToolContentPaths(raw string) []string {
	paths := make(map[string]struct{}, 4)
	for line := range strings.SplitSeq(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if path := nestedReadToolContentPath(line); path != "" {
			paths[path] = struct{}{}
		}
	}
	return sortedNestedKeys(paths)
}

func nestedReadToolContentPath(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "Contents of ") || !strings.HasSuffix(trimmed, ":") {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "Contents of "), ":"))
}

func isNestedReadTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read", "fileread", "file_read", "readfile", "file_read_tool":
		return true
	default:
		return false
	}
}
