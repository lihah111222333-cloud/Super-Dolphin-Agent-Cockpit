package orchestration

import (
	"context"
	"errors"
	"fmt"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// HandleTerminal 将终态路由到唯一 service owner。
func (s *service) HandleTerminal(ctx context.Context, terminal turndto.TurnTerminalV2) {
	s.handleRemoteTurnTerminal(ctx, terminal)
}

// HandleTerminalReceipt 返回 canonical 终态接收/持久化错误；仅作为内部 composition seam。
func (s *service) HandleTerminalReceipt(ctx context.Context, terminal turndto.TurnTerminalV2) error {
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return fmt.Errorf("remote canonical terminal contract: %w", err)
	}
	// 可确认 provider 接缝必须 fail-closed：缺少 durable terminal owner 时不
	// 得 ACK canonical terminal；旧的 void HandleTerminal 仍保留测试/通知兼容性。
	if s == nil || s.terminalOutcomes == nil {
		return errors.New("terminal outcome receipt store is not configured")
	}
	return s.handleRemoteTurnTerminalResult(ctx, terminal)
}
