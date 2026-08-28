package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// DiagnosticsCacheKey 是 diagnostics 结果的内容寻址身份。
// 三个维度必须同时相等才允许复用，避免旧文件或旧 server 结果污染新请求。
type DiagnosticsCacheKey struct {
	NormalizedArgs   string
	TargetFileDigest string
	ServerGeneration string
}

// DiagnosticsCacheDecision 是无副作用的缓存命中裁决及其原因。
type DiagnosticsCacheDecision struct {
	Reuse  bool
	Reason string
}

// BuildDiagnosticsCacheKey 根据规范化参数、文件 digest 和 server generation 构造键。
func BuildDiagnosticsCacheKey(arguments any, targetFileDigest, serverGeneration string) (DiagnosticsCacheKey, error) {
	normalized, err := normalizeDiagnosticsCacheArguments(arguments)
	if err != nil {
		return DiagnosticsCacheKey{}, err
	}
	if strings.TrimSpace(targetFileDigest) == "" {
		return DiagnosticsCacheKey{}, errors.New("diagnostics target file digest is required")
	}
	if strings.TrimSpace(serverGeneration) == "" {
		return DiagnosticsCacheKey{}, errors.New("diagnostics server generation is required")
	}
	return DiagnosticsCacheKey{
		NormalizedArgs:   normalized,
		TargetFileDigest: strings.TrimSpace(targetFileDigest),
		ServerGeneration: strings.TrimSpace(serverGeneration),
	}, nil
}

// DecideDiagnosticsCache 只比较完整键，不读取文件、不访问 server，也不修改缓存。
func DecideDiagnosticsCache(cached *DiagnosticsCacheKey, requested DiagnosticsCacheKey) DiagnosticsCacheDecision {
	if cached == nil {
		return DiagnosticsCacheDecision{Reason: "no_cached_result"}
	}
	if cached.NormalizedArgs != requested.NormalizedArgs {
		return DiagnosticsCacheDecision{Reason: "normalized_args_changed"}
	}
	if cached.TargetFileDigest != requested.TargetFileDigest {
		return DiagnosticsCacheDecision{Reason: "target_file_digest_changed"}
	}
	if cached.ServerGeneration != requested.ServerGeneration {
		return DiagnosticsCacheDecision{Reason: "server_generation_changed"}
	}
	return DiagnosticsCacheDecision{Reuse: true, Reason: "cache_hit"}
}

// diagnosticsResultCache 保存单个 handler 实例内的只读 diagnostics 快照。
type diagnosticsResultCache struct {
	mu       sync.Mutex
	capacity int
	clock    uint64
	entries  map[DiagnosticsCacheKey]diagnosticsCacheEntry
}

type diagnosticsCacheEntry struct {
	value    diagnosticsCacheValue
	lastUsed uint64
}

type diagnosticsCacheValue struct {
	Items   []protocol.PublishDiagnosticsParams
	Message string
}

const diagnosticsResultCacheCapacity = 64

func newDiagnosticsResultCache() *diagnosticsResultCache {
	return &diagnosticsResultCache{
		capacity: diagnosticsResultCacheCapacity,
		entries:  make(map[DiagnosticsCacheKey]diagnosticsCacheEntry, diagnosticsResultCacheCapacity),
	}
}

func (c *diagnosticsResultCache) get(key DiagnosticsCacheKey) (diagnosticsCacheValue, bool) {
	if c == nil {
		return diagnosticsCacheValue{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return diagnosticsCacheValue{}, false
	}
	entry.lastUsed = c.nextCacheClock()
	c.entries[key] = entry
	entry.value.Items = cloneDiagnosticsPublishItems(entry.value.Items)
	return entry.value, true
}

func (c *diagnosticsResultCache) put(key DiagnosticsCacheKey, value diagnosticsCacheValue) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacity <= 0 {
		return
	}
	value.Items = cloneDiagnosticsPublishItems(value.Items)
	entry := diagnosticsCacheEntry{value: value, lastUsed: c.nextCacheClock()}
	c.entries[key] = entry
	c.evictDiagnosticsCacheEntries()
}

func (c *diagnosticsResultCache) nextCacheClock() uint64 {
	c.clock++
	return c.clock
}

// evictDiagnosticsCacheEntries 按最近使用时间淘汰最旧结果，容量满时保持上限。
func (c *diagnosticsResultCache) evictDiagnosticsCacheEntries() {
	for len(c.entries) > c.capacity {
		var oldestKey DiagnosticsCacheKey
		var oldest diagnosticsCacheEntry
		first := true
		for key, entry := range c.entries {
			if first || entry.lastUsed < oldest.lastUsed || (entry.lastUsed == oldest.lastUsed && diagnosticsCacheKeyLess(key, oldestKey)) {
				oldestKey, oldest, first = key, entry, false
			}
		}
		if first {
			return
		}
		delete(c.entries, oldestKey)
	}
}

func diagnosticsCacheKeyLess(left, right DiagnosticsCacheKey) bool {
	if left.NormalizedArgs != right.NormalizedArgs {
		return left.NormalizedArgs < right.NormalizedArgs
	}
	if left.TargetFileDigest != right.TargetFileDigest {
		return left.TargetFileDigest < right.TargetFileDigest
	}
	return left.ServerGeneration < right.ServerGeneration
}

func (c *diagnosticsResultCache) size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// diagnosticsTargetFileDigest 对目标文件内容按 URI 排序后求一个聚合 digest。
func diagnosticsTargetFileDigest(targets []diagnosticTarget) (string, error) {
	if len(targets) == 0 {
		return "", errors.New("diagnostics target file digest requires at least one target")
	}
	type targetDigest struct {
		URI    string `json:"uri"`
		Digest string `json:"digest"`
	}
	items := make([]targetDigest, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		uri := strings.TrimSpace(target.URI)
		if uri == "" {
			return "", errors.New("diagnostics target URI is required for file digest")
		}
		if _, ok := seen[uri]; ok {
			return "", fmt.Errorf("duplicate diagnostics target URI %q", uri)
		}
		seen[uri] = struct{}{}
		payload, err := os.ReadFile(target.AbsPath)
		if err != nil {
			return "", fmt.Errorf("read diagnostics target %s for digest: %w", target.DisplayPath, err)
		}
		if len(payload) > maxDiagnosticFileBytes {
			return "", fmt.Errorf("diagnostics target %s exceeds %d bytes", target.DisplayPath, maxDiagnosticFileBytes)
		}
		digest := sha256.Sum256(payload)
		items = append(items, targetDigest{URI: uri, Digest: hex.EncodeToString(digest[:])})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].URI < items[j].URI })
	raw, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("marshal diagnostics target digests: %w", err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// diagnosticsCacheArguments 构造参与缓存身份的规范化请求参数。
func diagnosticsCacheArguments(ctx context.Context, input fileToolInput, targets []diagnosticTarget, filter *diagnosticsFilterSpec) map[string]any {
	uris := make([]string, 0, len(targets))
	for _, target := range targets {
		uris = append(uris, target.URI)
	}
	sort.Strings(uris)
	arguments := map[string]any{
		"file_uris":   uris,
		"language_id": strings.ToLower(strings.TrimSpace(input.LanguageID)),
	}
	if scope, ok := common.ToolScopeFromContext(ctx); ok {
		arguments["scope"] = map[string]any{
			"agent_id":  strings.TrimSpace(scope.AgentID),
			"thread_id": strings.TrimSpace(scope.ThreadID),
			"cwd":       strings.TrimSpace(scope.CWD),
			"family":    strings.TrimSpace(scope.Family),
		}
	}
	if filter == nil {
		return arguments
	}
	changed := make(map[string][]int, len(filter.ChangedLinesByURI))
	for uri, lines := range filter.ChangedLinesByURI {
		values := make([]int, 0, len(lines))
		for line := range lines {
			values = append(values, line)
		}
		sort.Ints(values)
		changed[uri] = values
	}
	arguments["diff_filter"] = map[string]any{
		"mode":          filter.Mode,
		"changed_lines": changed,
	}
	return arguments
}

func normalizeDiagnosticsCacheArguments(arguments any) (string, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("marshal diagnostics cache arguments: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("decode diagnostics cache arguments: %w", err)
	}
	normalized := normalizeDiagnosticsCacheJSON(value)
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode normalized diagnostics cache arguments: %w", err)
	}
	return string(canonical), nil
}

// normalizeDiagnosticsCacheJSON 递归规范化数组和对象中不影响语义的顺序。
func normalizeDiagnosticsCacheJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := normalizeDiagnosticsCacheJSON(child)
			if key == "file_paths" {
				normalized = sortNormalizedStringArray(normalized)
			}
			if key == "changed_lines" {
				normalized = normalizeDiagnosticsChangedLines(normalized)
			}
			typed[key] = normalized
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = normalizeDiagnosticsCacheJSON(child)
		}
		return typed
	default:
		return value
	}
}

func sortNormalizedStringArray(value any) any {
	values, ok := value.([]any)
	if !ok {
		return value
	}
	sort.Slice(values, func(i, j int) bool {
		left, leftOK := values[i].(string)
		right, rightOK := values[j].(string)
		if !leftOK || !rightOK {
			return fmt.Sprint(values[i]) < fmt.Sprint(values[j])
		}
		return left < right
	})
	return values
}

// normalizeDiagnosticsChangedLines 对差量行号数组排序，确保等价请求共享缓存键。
func normalizeDiagnosticsChangedLines(value any) any {
	values, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for path, lines := range values {
		array, ok := lines.([]any)
		if !ok {
			continue
		}
		sort.Slice(array, func(i, j int) bool {
			left, leftOK := array[i].(float64)
			right, rightOK := array[j].(float64)
			if !leftOK || !rightOK {
				return fmt.Sprint(array[i]) < fmt.Sprint(array[j])
			}
			return left < right
		})
		values[path] = array
	}
	return values
}

func diagnosticsServerGeneration(registry interface{ CurrentDiagnosticGeneration() uint64 }) string {
	if registry == nil {
		return ""
	}
	return strconv.FormatUint(registry.CurrentDiagnosticGeneration(), 10)
}

func cloneDiagnosticsPublishItems(items []protocol.PublishDiagnosticsParams) []protocol.PublishDiagnosticsParams {
	cloned := make([]protocol.PublishDiagnosticsParams, 0, len(items))
	for _, item := range items {
		copyItem := item
		copyItem.Diagnostics = append([]protocol.Diagnostic(nil), item.Diagnostics...)
		cloned = append(cloned, copyItem)
	}
	return cloned
}
