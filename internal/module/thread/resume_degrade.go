package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// 不可恢复的 resume 失败（provider 会话历史已丢失）走降级路径。
//
// 当 provider 侧 rollout/历史文件已经不存在时，重试恢复永远不可能成功；
// 继续保留 binding 和 resumeInFlight 只会让线程永远卡在“无法恢复”的死局里。
// 降级路径清理 binding、把线程标记为 failed 并发布 stopped 事件，
// 让用户可以通过新建会话重新开始，而不是被旧 binding 永久堵死。

// isUnrecoverableResumeError 判断 resume 错误是否表示 provider 会话历史不可恢复地丢失。
// 匹配片段覆盖远端 codex app-server 的 "no rollout found"、本地 rollout 扫描的
// "rollout not found" 以及历史读取的 "persisted thread history not found"。
// 只有这类错误才允许降级清理 binding；网络、认证、超时等暂时性失败必须原样保留，
// 等待后续重试机会，不能因为一次波动就破坏线程的可恢复性。
func isUnrecoverableResumeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{"no rollout found", "rollout not found", "persisted thread history not found"} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// degradeLostResume 处理不可恢复的 resume 失败。
// 它仅在 binding 删除和线程状态更新全部成功后发布 stopped 事件并清除
// resumeInFlight；任一步失败都会返回错误并保留进程内标记，避免宣告虚假终态。
func (s *service) degradeLostResume(ctx context.Context, threadID, agentID string, cause error) error {
	threadID = strings.TrimSpace(threadID)
	agentID = strings.TrimSpace(agentID)
	if s == nil {
		return errors.New("thread: service is required to degrade lost resume")
	}

	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil && !errors.Is(err, contract.ErrNotFound) {
		return fmt.Errorf("thread: resolve binding for degraded resume: %w", err)
	}
	if binding != nil {
		if err := s.deleteThreadBinding(ctx, binding); err != nil {
			return fmt.Errorf("thread: delete binding for degraded resume: %w", err)
		}
	}
	if err := s.updateThreadStatus(ctx, threadID, statusFailed); err != nil {
		return fmt.Errorf("thread: mark degraded resume thread failed: %w", err)
	}
	s.resumeInFlight.Delete(agentID)
	if s.logger != nil {
		s.logger.Warn("thread: degraded unrecoverable resume",
			"thread_id", threadID,
			"agent_id", agentID,
			"error", cause,
		)
	}
	s.publishThreadStopped(threadID, agentID, statusFailed, "provider session history lost; start a new session")
	return nil
}

// resumeLostError 包装不可恢复的 resume 失败为对用户可读的错误。
// 原始 provider 错误仍保留在错误链里，前端可以基于稳定文本识别历史丢失。
func resumeLostError(cause error) error {
	return fmt.Errorf("%w; provider session history lost, start a new session", cause)
}
