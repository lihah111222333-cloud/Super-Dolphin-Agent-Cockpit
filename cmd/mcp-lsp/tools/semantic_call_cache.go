package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
)

const semanticCallTargetMaxBytes = 8 << 20

// semanticCallDeduper 合并同一 handler 实例内相同语义请求的并发执行。
// 只有 in-flight 项暂存于 map，完成后立即删除，不把失败或旧结果当作缓存。
type semanticCallDeduper struct {
	mu      sync.Mutex
	flights map[string]*semanticCallFlight
}

type semanticCallFlight struct {
	done    chan struct{}
	result  any
	err     error
	waiters int
}

func newSemanticCallDeduper() *semanticCallDeduper {
	return &semanticCallDeduper{flights: make(map[string]*semanticCallFlight)}
}

// do 让同 key 的等待者复用 leader 的结果，并响应等待者自己的取消信号。
func (d *semanticCallDeduper) do(ctx context.Context, key string, fn func() (any, error)) (any, error) {
	if d == nil || strings.TrimSpace(key) == "" {
		return fn()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	flight, existing := d.flights[key]
	if !existing {
		flight = &semanticCallFlight{done: make(chan struct{})}
		d.flights[key] = flight
	} else {
		flight.waiters++
	}
	d.mu.Unlock()
	if existing {
		select {
		case <-flight.done:
			return flight.result, flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	result, err := fn()
	d.mu.Lock()
	flight.result = result
	flight.err = err
	delete(d.flights, key)
	close(flight.done)
	d.mu.Unlock()
	return result, err
}

func (d *semanticCallDeduper) waiterCount(key string) int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if flight := d.flights[key]; flight != nil {
		return flight.waiters
	}
	return 0
}

// deduplicateSemanticToolHandler 在 middleware 外层生成安全语义键，保留原 handler 签名。
func deduplicateSemanticToolHandler(tool string, registry lspmanager.Registry, handler ToolHandler) ToolHandler {
	deduper := newSemanticCallDeduper()
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		scoped, err := contextWithExplicitToolWorkDir(ctx, params)
		if err != nil {
			return nil, err
		}
		key, eligible, err := buildSemanticCallKey(scoped, tool, params, registry)
		if err != nil {
			return nil, err
		}
		if !eligible {
			return handler(ctx, params)
		}
		return deduper.do(scoped, key, func() (any, error) {
			return handler(ctx, params)
		})
	}
}

// buildSemanticCallKey 组合规范化参数、目标文件内容摘要和 server generation。
func buildSemanticCallKey(ctx context.Context, tool string, params json.RawMessage, registry lspmanager.Registry) (string, bool, error) {
	value, err := normalizedSemanticCallArguments(params)
	if err != nil {
		var objectError semanticCallArgumentsNotObjectError
		if errors.As(err, &objectError) {
			return "", false, nil
		}
		return "", false, err
	}
	paths := semanticCallTargetPaths(tool, value)
	if len(paths) == 0 {
		return "", false, nil
	}
	targetDigest, ok := semanticCallTargetDigest(ctx, paths)
	if !ok {
		return "", false, nil
	}
	generation := "0"
	if registry != nil {
		generation = strconv.FormatUint(registry.CurrentDiagnosticGeneration(), 10)
	}
	identity := struct {
		Tool             string `json:"tool"`
		Arguments        any    `json:"arguments"`
		TargetDigest     string `json:"target_digest"`
		ServerGeneration string `json:"server_generation"`
	}{Tool: tool, Arguments: value, TargetDigest: targetDigest, ServerGeneration: generation}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", false, fmt.Errorf("marshal semantic call key: %w", err)
	}
	return string(raw), true, nil
}

type semanticCallArgumentsNotObjectError struct{}

// Error 标识参数不是 JSON object，调用方会跳过去重并保留原 schema 错误。
func (semanticCallArgumentsNotObjectError) Error() string {
	return "semantic call arguments are not an object"
}

func normalizedSemanticCallArguments(params json.RawMessage) (map[string]any, error) {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, semanticCallArgumentsNotObjectError{}
	}
	return normalizeSemanticCallObject(object), nil
}

func normalizeSemanticCallObject(object map[string]any) map[string]any {
	for key, child := range object {
		object[key] = normalizeSemanticCallValue(key, child)
	}
	return object
}

// normalizeSemanticCallValue 递归规范化路径数组和 changed_lines 行号顺序。
func normalizeSemanticCallValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		normalized := normalizeSemanticCallObject(typed)
		if key == "changed_lines" {
			for path, child := range normalized {
				if lines, ok := child.([]any); ok {
					sort.Slice(lines, func(i, j int) bool { return fmt.Sprint(lines[i]) < fmt.Sprint(lines[j]) })
					normalized[path] = lines
				}
			}
		}
		return normalized
	case []any:
		for index, child := range typed {
			typed[index] = normalizeSemanticCallValue(key, child)
		}
		if key == "file_paths" {
			sort.Slice(typed, func(i, j int) bool { return fmt.Sprint(typed[i]) < fmt.Sprint(typed[j]) })
		}
		return typed
	default:
		return value
	}
}

// semanticCallTargetPaths 提取三类语义工具实际绑定的文件路径。
func semanticCallTargetPaths(tool string, value map[string]any) []string {
	switch strings.TrimSpace(tool) {
	case "structure":
		return semanticCallStringPaths(value["file_path"])
	case "xref":
		pos, _ := value["pos"].(string)
		path, _, _, _, err := parseFilePos(pos, false)
		if err != nil {
			return nil
		}
		return []string{path}
	case "diagnostics":
		if paths := semanticCallStringPaths(value["file_path"]); len(paths) > 0 {
			return paths
		}
		return semanticCallStringPaths(value["file_paths"])
	default:
		return nil
	}
}

// semanticCallStringPaths 将单路径或路径数组统一转为非空字符串列表。
func semanticCallStringPaths(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return []string{typed}
		}
	case []any:
		paths := make([]string, 0, len(typed))
		for _, item := range typed {
			if path, ok := item.(string); ok && strings.TrimSpace(path) != "" {
				paths = append(paths, path)
			}
		}
		return paths
	}
	return nil
}

// semanticCallTargetDigest 对目标文件内容按绝对路径排序后求聚合摘要。
func semanticCallTargetDigest(ctx context.Context, paths []string) (string, bool) {
	type targetDigest struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
	}
	items := make([]targetDigest, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		pathInfo, err := toolResolvePath(ctx, rawPath)
		if err != nil {
			return "", false
		}
		if _, duplicate := seen[pathInfo.AbsPath]; duplicate {
			continue
		}
		seen[pathInfo.AbsPath] = struct{}{}
		info, err := os.Stat(pathInfo.AbsPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > semanticCallTargetMaxBytes {
			return "", false
		}
		content, err := readSemanticCallTarget(pathInfo.AbsPath, info.Size())
		if err != nil {
			return "", false
		}
		digest := sha256.Sum256(content)
		items = append(items, targetDigest{Path: pathInfo.AbsPath, Digest: hex.EncodeToString(digest[:])})
	}
	if len(items) == 0 {
		return "", false
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	raw, err := json.Marshal(items)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), true
}

func readSemanticCallTarget(path string, size int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, size+1))
}
