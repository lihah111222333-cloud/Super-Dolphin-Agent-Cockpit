package memory

import (
	"context"
	encjson "encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/creachadair/jrpc2/handler"
)

// --- Phase 1.6 · auto-continue state RPC ---------------------------------
//
// 专用 UI RPC，让前端 watchdog/auto-continue 持久化 thread 级 state（manual
// abort 抑制位、watchdog 累计计数等）。路径硬约束在
// `_internal/auto-continue/state/<threadId>.json`，schema 校验
// schemaVersion=1 + threadId 与 path 一致；底层走 sharedfile store
// Reader/Upserter/Deleter，不复用 MCP shared_file_write（agent 工具语义不同）。

const autoContinueStatePathPrefix = "_internal/auto-continue/state/"
const autoContinueStatePathSuffix = ".json"
const autoContinueStateUpdatedBy = "auto-continue"
const autoContinueStateSchemaVersion = 1

// threadId 段允许字母、数字、`_`、`-`；首字符必须是字母或数字（防点开头/隐藏文件等）。
var autoContinueStateThreadIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_\-]*$`)

type uiAutoContinueStateGetParams struct {
	Path string `json:"path"`
}

type uiAutoContinueStateUpsertParams struct {
	Path     string `json:"path"`
	ThreadID string `json:"threadId"`
	Content  string `json:"content"`
}

type uiAutoContinueStateDeleteParams struct {
	Path string `json:"path"`
}

type UIAutoContinueStateDetail struct {
	Path      string    `json:"path"`
	ThreadID  string    `json:"threadId"`
	Content   string    `json:"content,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// validateAutoContinueStatePath 校验 path 形如
// `_internal/auto-continue/state/<threadId>.json`，返回 cleaned path 与
// 提取出的 threadId 段；任一规则违反返回 publicValidationErr。
func validateAutoContinueStatePath(rawPath string) (string, string, error) {
	cleaned := strings.TrimSpace(rawPath)
	if cleaned == "" {
		return "", "", publicValidationErr("path is required")
	}
	if strings.ContainsAny(cleaned, "\\\x00") {
		return "", "", publicValidationErr("path contains invalid characters")
	}
	if strings.Contains(cleaned, "..") {
		return "", "", publicValidationErr("path traversal not allowed")
	}
	if !strings.HasPrefix(cleaned, autoContinueStatePathPrefix) {
		return "", "", publicValidationErr("path must be under _internal/auto-continue/state/")
	}
	rest := cleaned[len(autoContinueStatePathPrefix):]
	if !strings.HasSuffix(rest, autoContinueStatePathSuffix) {
		return "", "", publicValidationErr("path must end with .json")
	}
	threadID := strings.TrimSuffix(rest, autoContinueStatePathSuffix)
	if threadID == "" {
		return "", "", publicValidationErr("threadId segment is empty")
	}
	if strings.ContainsRune(threadID, '/') {
		return "", "", publicValidationErr("threadId segment must not contain /")
	}
	if !autoContinueStateThreadIDPattern.MatchString(threadID) {
		return "", "", publicValidationErr("threadId segment has invalid characters")
	}
	return cleaned, threadID, nil
}

type autoContinueStatePayload struct {
	SchemaVersion int    `json:"schemaVersion"`
	ThreadID      string `json:"threadId"`
}

// validateAutoContinueStateContent 解析 content 为 JSON，校验
// schemaVersion=1 且 threadId 与 path 中 threadId 段一致。
func validateAutoContinueStateContent(content string, expectedThreadID string) error {
	if strings.TrimSpace(content) == "" {
		return publicValidationErr("content is required")
	}
	var payload autoContinueStatePayload
	if err := encjson.Unmarshal([]byte(content), &payload); err != nil {
		return publicValidationErr("content is not valid JSON")
	}
	if payload.SchemaVersion != autoContinueStateSchemaVersion {
		return publicValidationErr(fmt.Sprintf("schemaVersion must be %d", autoContinueStateSchemaVersion))
	}
	if payload.ThreadID != expectedThreadID {
		return publicValidationErr("payload threadId must match path threadId")
	}
	return nil
}

func getAutoContinueState(ctx context.Context, deps memoryHandlerDeps, req uiAutoContinueStateGetParams) (UIAutoContinueStateDetail, error) {
	if deps.SharedFiles == nil {
		return UIAutoContinueStateDetail{}, errors.New("shared file store is not configured")
	}
	cleaned, threadID, err := validateAutoContinueStatePath(req.Path)
	if err != nil {
		return UIAutoContinueStateDetail{}, err
	}
	item, err := deps.SharedFiles.Get(ctx, cleaned)
	if err != nil {
		return UIAutoContinueStateDetail{}, err
	}
	return UIAutoContinueStateDetail{
		Path:      item.Path,
		ThreadID:  threadID,
		Content:   item.Content,
		UpdatedBy: item.UpdatedBy,
		UpdatedAt: item.UpdatedAt,
	}, nil
}

func upsertAutoContinueState(ctx context.Context, deps memoryHandlerDeps, req uiAutoContinueStateUpsertParams) (UIAutoContinueStateDetail, error) {
	if deps.SharedFilesUpserter == nil {
		return UIAutoContinueStateDetail{}, errors.New("shared file store is not configured for upsert")
	}
	cleaned, pathThreadID, err := validateAutoContinueStatePath(req.Path)
	if err != nil {
		return UIAutoContinueStateDetail{}, err
	}
	payloadThreadID := strings.TrimSpace(req.ThreadID)
	if payloadThreadID == "" {
		return UIAutoContinueStateDetail{}, publicValidationErr("threadId is required")
	}
	if payloadThreadID != pathThreadID {
		return UIAutoContinueStateDetail{}, publicValidationErr("threadId must match path threadId")
	}
	if err := validateAutoContinueStateContent(req.Content, pathThreadID); err != nil {
		return UIAutoContinueStateDetail{}, err
	}
	item, err := deps.SharedFilesUpserter.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      cleaned,
		Content:   req.Content,
		UpdatedBy: autoContinueStateUpdatedBy,
	})
	if err != nil {
		return UIAutoContinueStateDetail{}, err
	}
	return UIAutoContinueStateDetail{
		Path:      item.Path,
		ThreadID:  pathThreadID,
		Content:   item.Content,
		UpdatedBy: item.UpdatedBy,
		UpdatedAt: item.UpdatedAt,
	}, nil
}

func deleteAutoContinueState(ctx context.Context, deps memoryHandlerDeps, req uiAutoContinueStateDeleteParams) (bool, error) {
	if deps.SharedFilesDeleter == nil {
		return false, errors.New("shared file store is not configured for deletion")
	}
	cleaned, _, err := validateAutoContinueStatePath(req.Path)
	if err != nil {
		return false, err
	}
	count, err := deps.SharedFilesDeleter.Delete(ctx, cleaned)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// registerAutoContinueStateHandlers returns the three RPC routes for the
// Phase 1.6 auto-continue state contract. Called from
// registerUIMemoryMutationHandlers so the routes live in the same handler
// map without bloating the main file.
func registerAutoContinueStateHandlers(p memoryHandlerDeps) handler.Map {
	return handler.Map{
		"ui/auto-continue/state/get": platformrpc.StrictHandler(func(ctx context.Context, req uiAutoContinueStateGetParams) (UIAutoContinueStateDetail, error) {
			return getAutoContinueState(ctx, p, req)
		}),
		"ui/auto-continue/state/upsert": platformrpc.StrictHandler(func(ctx context.Context, req uiAutoContinueStateUpsertParams) (UIAutoContinueStateDetail, error) {
			return upsertAutoContinueState(ctx, p, req)
		}),
		"ui/auto-continue/state/delete": platformrpc.StrictHandler(func(ctx context.Context, req uiAutoContinueStateDeleteParams) (map[string]any, error) {
			deleted, err := deleteAutoContinueState(ctx, p, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{"deleted": deleted}, nil
		}),
	}
}
