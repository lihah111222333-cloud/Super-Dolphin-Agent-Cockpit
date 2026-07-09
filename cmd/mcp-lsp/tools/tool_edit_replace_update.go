package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
)

// applyReplaceRangeUpdate 先写磁盘，再等待 LSP 同步或 git diff 确认。
// 两条确认路径都失败才回滚，避免 LSP 慢响应时把已经真实落盘的编辑误判为失败。
func (h EditHandler) applyReplaceRangeUpdate(ctx context.Context, manager lspmanager.Manager, path string, file editableFile, updatedContent string, version int, log *editStageLogger) (bool, string, error) {
	stage := log.Started("write_file", "bytes", len(updatedContent), "version", version)
	if err := atomicReplaceFile(path, []byte(updatedContent), file.mode, defaultFileWriter); err != nil {
		log.Failed("write_file", stage, err)
		return false, "", err
	}
	log.Completed("write_file", stage)
	if manager == nil {
		log.Skipped("lsp_sync", "manager_nil")
		return false, "", nil
	}
	syncC, diffC, cancelSync := h.startReplaceConfirmations(ctx, manager, path, updatedContent, version, log)
	defer cancelSync()
	decision := waitReplaceConfirmation(ctx, path, syncC, diffC, cancelSync, log)
	if decision.err == nil {
		return decision.lspSync, decision.warning, nil
	}
	return h.rollbackReplaceRangeUpdate(ctx, manager, path, file, version, decision.err, log)
}

// startReplaceConfirmations 并行启动 LSP 同步和磁盘 diff 确认。
// 任何一方先证明编辑有效，waitReplaceConfirmation 都可以结束等待。
func (h EditHandler) startReplaceConfirmations(ctx context.Context, manager lspmanager.Manager, path string, updatedContent string, version int, log *editStageLogger) (<-chan replaceSyncResult, <-chan editDiskConfirmResult, context.CancelFunc) {
	syncCtx, cancelSync := platformconfig.WithTimeout(ctx, editLSPSyncTimeout)
	syncC := make(chan replaceSyncResult, 1)
	diffC := make(chan editDiskConfirmResult, 1)
	safego.Go(syncCtx, nil, "mcp-lsp.patch_edit.lsp-sync", func(context.Context) {
		stage := log.Started("lsp_sync", "timeout_ms", editLSPSyncTimeout.Milliseconds(), "version", version, "content_bytes", len(updatedContent))
		lspSync, warning, err := h.syncDocument(syncCtx, manager, path, updatedContent, version)
		if err != nil {
			log.Failed("lsp_sync", stage, err)
		} else {
			log.Completed("lsp_sync", stage, "lsp_sync", lspSync, "warning", warning != "")
		}
		syncC <- replaceSyncResult{lspSync: lspSync, warning: warning, err: err}
	})
	safego.Go(ctx, nil, "mcp-lsp.patch_edit.disk-confirm", func(context.Context) {
		stage := log.Started("disk_confirm", "timeout_ms", editDiskConfirmTimeout.Milliseconds())
		result := confirmEditDiskWriteWithGitDiff(path, updatedContent)
		if result.confirmed {
			log.Completed("disk_confirm", stage, "diff_bytes", result.diffBytes, "warning", result.warning != "")
		} else {
			log.Failed("disk_confirm", stage, result.err)
		}
		diffC <- result
	})
	return syncC, diffC, cancelSync
}

// waitReplaceConfirmation 等待 LSP 或磁盘确认 replace 操作。
// git diff 先成功时会取消 LSP 等待并写恢复日志，保证工具返回能反映真实落盘状态。
func waitReplaceConfirmation(ctx context.Context, path string, syncC <-chan replaceSyncResult, diffC <-chan editDiskConfirmResult, cancelSync context.CancelFunc, log *editStageLogger) replaceUpdateDecision {
	stage := log.Started("wait_confirmation")
	var syncErr error
	var diffErr error
	for pending := 2; pending > 0; pending-- {
		select {
		case result := <-syncC:
			if result.err == nil {
				log.Completed("wait_confirmation", stage, "source", "lsp_sync", "lsp_sync", result.lspSync, "warning", result.warning != "")
				return replaceUpdateDecision{lspSync: result.lspSync, warning: result.warning}
			}
			syncErr = result.err
		case result := <-diffC:
			if result.confirmed {
				cancelSync()
				logEditDiskConfirmation(path, result.diffBytes, syncErr)
				log.Completed("wait_confirmation", stage, "source", "git_diff", "diff_bytes", result.diffBytes, "sync_error", syncErr != nil)
				return replaceUpdateDecision{warning: result.warning}
			}
			diffErr = result.err
		case <-ctx.Done():
			log.Failed("wait_confirmation", stage, ctx.Err())
			return replaceUpdateDecision{err: ctx.Err()}
		}
	}
	err := firstReplaceConfirmationError(syncErr, diffErr)
	log.Failed("wait_confirmation", stage, err, "sync_error", syncErr != nil, "disk_confirm_error", diffErr != nil)
	return replaceUpdateDecision{err: err}
}

// firstReplaceConfirmationError 选择最能说明失败原因的确认错误。
func firstReplaceConfirmationError(syncErr error, diffErr error) error {
	if syncErr == nil {
		syncErr = diffErr
	}
	if syncErr == nil {
		syncErr = errors.New("LSP sync failed and git diff did not confirm disk write")
	}
	return syncErr
}

// rollbackReplaceRangeUpdate 尝试恢复原始磁盘内容并同步回 LSP。
// 回滚失败会被合并进返回错误，避免调用方误以为文件仍处于原始状态。
func (h EditHandler) rollbackReplaceRangeUpdate(ctx context.Context, manager lspmanager.Manager, path string, file editableFile, version int, syncErr error, log *editStageLogger) (bool, string, error) {
	stage := log.Started("rollback", "version", version, "reason", syncErr != nil)
	rollbackErr := atomicReplaceFile(path, []byte(file.raw), file.mode, defaultFileWriter)
	if rollbackErr == nil {
		rollbackErr = h.syncRollbackDocument(ctx, manager, path, file.raw, version)
	}
	if rollbackErr != nil {
		log.Failed("rollback", stage, rollbackErr)
	} else {
		log.Completed("rollback", stage)
	}
	return false, "", withRollbackError(syncErr, rollbackErr)
}

// confirmEditDiskWriteWithGitDiff 确认文件内容等于请求内容且 git diff 非空。
// 它是 LSP sync 的兜底确认路径，但仍要求实际 diff 存在，避免 no-op 被当成成功写入。
func confirmEditDiskWriteWithGitDiff(path string, updatedContent string) editDiskConfirmResult {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), editDiskConfirmTimeout)
	defer cancel()
	raw, err := os.ReadFile(path)
	if err != nil {
		return editDiskConfirmResult{err: err}
	}
	if string(raw) != updatedContent {
		return editDiskConfirmResult{err: errors.New("disk content does not match requested edit")}
	}
	diff, err := gitDiffForFile(ctx, path)
	if err != nil {
		return editDiskConfirmResult{err: err}
	}
	if strings.TrimSpace(diff) == "" {
		return editDiskConfirmResult{err: errors.New("git diff is empty after edit")}
	}
	return editDiskConfirmResult{
		confirmed: true,
		warning:   fmt.Sprintf("LSP sync is still pending; disk write confirmed by git diff (%d bytes)", len(diff)),
		diffBytes: len(diff),
	}
}

// gitDiffForFile 在文件所在仓库根目录读取该文件的 diff。
func gitDiffForFile(ctx context.Context, path string) (string, error) {
	dir := filepath.Dir(path)
	rootRaw, err := runEditGitCommand(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := filepath.Clean(strings.TrimSpace(rootRaw))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return runEditGitCommand(ctx, root, "diff", "--", filepath.ToSlash(rel))
}

// runEditGitCommand 执行受超时控制的 git 子命令，并把 stderr 保留到错误文本。
func runEditGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), editDiskConfirmTimeout)
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return stdout.String(), nil
}
