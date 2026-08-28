package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// newContinuationInfo 构造无服务端状态的稳定续查描述。
// nextArgs 只使用目标工具已经声明的字段，避免截断后诱导调用方猜参数。
func newContinuationInfo(
	tool, action, reason string,
	nextArgs map[string]any,
	total, showing int,
) (*protocol.ContinuationInfo, error) {
	if tool == "" || action == "" || reason == "" {
		return nil, fmt.Errorf("continuation tool, action, and reason are required")
	}
	if nextArgs == nil {
		return nil, fmt.Errorf("continuation next_args are required")
	}
	if total <= showing {
		return nil, fmt.Errorf("continuation requires total > showing")
	}
	identity := struct {
		Tool     string         `json:"tool"`
		Action   string         `json:"action"`
		Reason   string         `json:"reason"`
		NextArgs map[string]any `json:"next_args"`
		Total    int            `json:"total"`
		Showing  int            `json:"showing"`
	}{tool, action, reason, nextArgs, total, showing}
	raw, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("marshal continuation identity: %w", err)
	}
	digest := sha256.Sum256(raw)
	return &protocol.ContinuationInfo{
		Token:    hex.EncodeToString(digest[:]),
		Reason:   reason,
		NextArgs: nextArgs,
	}, nil
}

// appendContinuationRecord 将续查字段加入稳定行协议，供模型和审计器解析。
func appendContinuationRecord(lines []string, continuation *protocol.ContinuationInfo) []string {
	if continuation == nil {
		return lines
	}
	nextArgs, err := json.Marshal(continuation.NextArgs)
	if err != nil {
		return append(lines, lineprotocol.ErrorLine("continuation_encode_failed", false))
	}
	return append(lines, lineprotocol.FieldsRecord("CONTINUATION",
		lineprotocol.Field{Key: "token", Value: continuation.Token},
		lineprotocol.Field{Key: "reason", Value: continuation.Reason},
		lineprotocol.Field{Key: "next_args", Value: string(nextArgs)},
		lineprotocol.Field{Key: "truncated", Value: strconv.FormatBool(true)},
	))
}

// nextResultLimit 返回不超过协议硬上限的下一次请求上限。
// 达到硬上限时返回零，调用方应依赖 continuation 的收窄参数而非盲目重试。
func nextResultLimit(current, hardLimit int) int {
	if current <= 0 || hardLimit <= current {
		return 0
	}
	next := current * 2
	if next <= current {
		next = current + 1
	}
	if next > hardLimit {
		next = hardLimit
	}
	return next
}

// documentSymbolContinuation 构造 document_symbol 截断后的合法重查参数。
func documentSymbolContinuation(req structureParams, total, showing, limit int) (*protocol.ContinuationInfo, error) {
	nextArgs := map[string]any{
		"action":    "document_symbol",
		"file_path": req.FilePath,
	}
	reason := "max_results"
	if next := nextResultLimit(limit, protocol.XRefResultLimit); next > 0 {
		nextArgs["max_results"] = next
	} else {
		reason = "hard_limit_narrow_file_or_symbol_scope"
	}
	return newContinuationInfo("structure", "document_symbol", reason, nextArgs, total, showing)
}

// workspaceSymbolContinuation 构造 workspace_symbol 截断后的合法重查参数。
func workspaceSymbolContinuation(req structureParams, matchMode string, total, showing, limit int) (*protocol.ContinuationInfo, error) {
	nextArgs := map[string]any{
		"action":     "workspace_symbol",
		"query":      req.Query,
		"match_mode": matchMode,
	}
	if req.FilePath != "" {
		nextArgs["file_path"] = req.FilePath
		if req.LanguageID != "" {
			nextArgs["language_id"] = req.LanguageID
		}
	} else if req.WorkspaceLanguage != "" {
		nextArgs["workspace_language"] = req.WorkspaceLanguage
	}
	reason := "max_results"
	if next := nextResultLimit(limit, protocol.XRefResultLimit); next > 0 {
		nextArgs["max_results"] = next
	} else {
		reason = "hard_limit_narrow_query_or_scope"
	}
	return newContinuationInfo("structure", "workspace_symbol", reason, nextArgs, total, showing)
}

// fileStructureContinuation 构造文件型 structure action 的统一截断续查参数。
func fileStructureContinuation(action, filePath string, total, showing, limit, hardLimit int) (*protocol.ContinuationInfo, error) {
	nextArgs := map[string]any{
		"action":    action,
		"file_path": filePath,
	}
	if next := nextResultLimit(limit, hardLimit); next > 0 {
		nextArgs["max_results"] = next
	}
	return newContinuationInfo("structure", action, "max_results", nextArgs, total, showing)
}

// referencesContinuation 构造 references 截断后的合法重查参数。
func referencesContinuation(req xrefParams, includeDeclaration bool, sampled sampledReferences, total, showing, limit int) (*protocol.ContinuationInfo, error) {
	nextArgs := map[string]any{
		"action": "references",
		"pos":    req.Pos,
	}
	if req.IncludeDeclaration != nil {
		nextArgs["include_declaration"] = includeDeclaration
	}
	if req.LanguageID != "" {
		nextArgs["language_id"] = req.LanguageID
	}
	if next := nextResultLimit(limit, protocol.XRefResultLimit); next > 0 {
		nextArgs["max_results"] = next
	}
	reason := "max_results"
	if sampled.externalSampling {
		reason = "external_sampling"
	}
	return newContinuationInfo("xref", "references", reason, nextArgs, total, showing)
}

// hierarchyContinuation 构造 call/type hierarchy 截断后的合法重查参数。
func hierarchyContinuation(action, pos, direction, languageID string, total, showing, limit int) (*protocol.ContinuationInfo, error) {
	nextArgs := map[string]any{
		"action": action,
		"pos":    pos,
	}
	if direction != "" {
		nextArgs["direction"] = direction
	}
	if languageID != "" {
		nextArgs["language_id"] = languageID
	}
	if next := nextResultLimit(limit, protocol.XRefResultLimit); next > 0 {
		nextArgs["max_results"] = next
	}
	return newContinuationInfo("xref", action, "max_results", nextArgs, total, showing)
}

// addReferencesContinuation 在引用列表截断时附加续查描述。
func addReferencesContinuation(response *protocol.GroupedLocationResult, req xrefParams, includeDeclaration bool, sampled sampledReferences, total, limit int) error {
	if response == nil || !response.Truncated {
		return nil
	}
	continuation, err := referencesContinuation(req, includeDeclaration, sampled, total, response.Showing, limit)
	if err != nil {
		return err
	}
	response.Continuation = continuation
	return nil
}

// addHierarchyContinuation 在调用或类型层级截断时附加续查描述。
func addHierarchyContinuation(response *hierarchyEdgeListResponse, action, pos, direction, languageID string, total, limit int) error {
	if response == nil || len(response.Rows) >= total {
		return nil
	}
	continuation, err := hierarchyContinuation(action, pos, direction, languageID, total, len(response.Rows), limit)
	if err != nil {
		return err
	}
	response.Continuation = continuation
	return nil
}
