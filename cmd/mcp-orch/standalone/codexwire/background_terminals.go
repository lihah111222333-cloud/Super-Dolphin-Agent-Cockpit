package codexwire

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type nativeThreadIdentity struct {
	ThreadID string `json:"threadId"`
}

// CleanBackgroundTerminals 通过原生thread所有权关闭工具会话，不对观察到的OS PID发送信号。
// 调用者必须先阻断该thread的新turn；turn/interrupt不调用此全thread清理入口。
func (p *Process) CleanBackgroundTerminals(ctx context.Context, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("background terminal cleanup requires thread identity")
	}
	var result map[string]any
	if err := p.Client.CallResult(ctx, "thread/backgroundTerminals/clean", nativeThreadIdentity{ThreadID: threadID}, &result); err != nil {
		return fmt.Errorf("clean Codex background terminals: %w", err)
	}
	if result == nil {
		return errors.New("background terminal cleanup acknowledgement missing")
	}
	return nil
}

// ArchiveThread 在thread卸载前清理其原生工具资源，成功后记录归档身份，Close无需再访问已卸载thread。
func (p *Process) ArchiveThread(ctx context.Context, threadID string) error {
	if err := p.CleanBackgroundTerminals(ctx, threadID); err != nil {
		return err
	}
	var result map[string]any
	if err := p.Client.CallResult(ctx, "thread/archive", nativeThreadIdentity{ThreadID: threadID}, &result); err != nil {
		return err
	}
	if result == nil {
		return errors.New("thread archive acknowledgement missing")
	}
	p.exitMu.Lock()
	p.archivedThreadID = threadID
	p.exitMu.Unlock()
	return nil
}
