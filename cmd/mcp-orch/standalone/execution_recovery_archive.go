package standalone

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

type recoveryRPCCall func(context.Context, string, any, any) error

type recoveryNativeThread struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
}
type recoveryThreadReply struct {
	Thread recoveryNativeThread `json:"thread"`
}
type recoveryThreadListParams struct {
	Archived       bool     `json:"archived"`
	CWD            string   `json:"cwd"`
	Limit          int      `json:"limit"`
	Cursor         string   `json:"cursor,omitempty"`
	SourceKinds    []string `json:"sourceKinds"`
	ModelProviders []string `json:"modelProviders"`
}
type recoveryThreadListReply struct {
	Data       *[]recoveryNativeThread `json:"data"`
	NextCursor *string                 `json:"nextCursor"`
}

// prepareArchivedCodexRecovery 先核对真实thread/cwd，再完整分页证明归档状态；只对已证明归档执行一次unarchive。
func prepareArchivedCodexRecovery(ctx context.Context, call recoveryRPCCall, threadID, authorizedCWD string) error {
	var read recoveryThreadReply
	if err := call(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, &read); err != nil {
		return err
	}
	if err := validateRecoveryNativeThread(read.Thread, threadID, authorizedCWD); err != nil {
		return err
	}
	archived, err := findArchivedRecoveryThread(ctx, call, threadID, read.Thread.CWD)
	if err != nil || !archived {
		return err
	}
	var restored recoveryThreadReply
	if err := call(ctx, "thread/unarchive", map[string]string{"threadId": threadID}, &restored); err != nil {
		return err
	}
	return validateRecoveryNativeThread(restored.Thread, threadID, authorizedCWD)
}

// validateRecoveryNativeThread 按canonical绝对cwd验证owner，拒绝外部thread及目录别名逃逸。
func validateRecoveryNativeThread(thread recoveryNativeThread, threadID, authorizedCWD string) error {
	if thread.ID != threadID || threadID == "" || !filepath.IsAbs(thread.CWD) || !filepath.IsAbs(authorizedCWD) {
		return errors.New("Codex recovery returned a foreign thread or cwd")
	}
	actual, err := filepath.EvalSymlinks(thread.CWD)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(authorizedCWD)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("Codex recovery cwd does not match authorized workspace")
	}
	return nil
}

// findArchivedRecoveryThread 显式覆盖managed来源及全部provider，分页超界或cursor异常立即失败。
func findArchivedRecoveryThread(ctx context.Context, call recoveryRPCCall, threadID, cwd string) (bool, error) {
	params := recoveryThreadListParams{Archived: true, CWD: cwd, Limit: 100, ModelProviders: []string{}, SourceKinds: []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"}}
	seen := map[string]bool{}
	found := false
	for page := 0; page < 128; page++ {
		var response recoveryThreadListReply
		if err := call(ctx, "thread/list", params, &response); err != nil {
			return false, err
		}
		if err := inspectRecoveryArchivePage(response, params, threadID, &found); err != nil {
			return false, err
		}
		if response.NextCursor == nil {
			return found, nil
		}
		cursor := *response.NextCursor
		if strings.TrimSpace(cursor) == "" || seen[cursor] {
			return false, errors.New("Codex recovery archive pagination has invalid cursor")
		}
		seen[cursor] = true
		params.Cursor = cursor
	}
	return false, errors.New("Codex recovery archive pagination exceeded bounded page count")
}

// inspectRecoveryArchivePage 拒绝缺失data、超量页面、错误cwd和重复目标，不把畸形响应当未归档。
func inspectRecoveryArchivePage(response recoveryThreadListReply, params recoveryThreadListParams, threadID string, found *bool) error {
	if response.Data == nil || len(*response.Data) > params.Limit {
		return errors.New("Codex recovery archive list has missing or oversized data")
	}
	for _, thread := range *response.Data {
		if thread.ID == "" || thread.CWD != params.CWD {
			return errors.New("Codex recovery archive list violated identity or cwd filter")
		}
		if thread.ID != threadID {
			continue
		}
		if *found {
			return errors.New("Codex recovery archive list duplicated target thread")
		}
		*found = true
	}
	return nil
}
