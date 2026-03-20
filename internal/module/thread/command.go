package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// thread/skills/list 通过这里的 thread 命令通道下沉，语义上返回 thread 绑定的 active skills。
// 与 skills/list 不同：后者扫描本地 skill 目录，返回所有已安装的 skill 元信息。
func (s *service) SendCommand(ctx context.Context, threadID, command, args string) (any, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	cmd := normalizeCommand(command)
	switch cmd {
	case "/model", "/personality", "/approvals":
		patch, err := commandPatch(cmd, args)
		if err != nil {
			return nil, err
		}
		if err := session.Configure(ctx, patch); err != nil {
			return nil, err
		}
	case "/interrupt":
		if err := session.Interrupt(ctx, dto.InterruptRequest{
			ThreadID: historyTargetID(binding, threadID),
			Source:   strings.TrimSpace(args),
		}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported command: %s", cmd)
	}
	return map[string]any{
		"command":  cmd,
		"threadId": strings.TrimSpace(threadID),
	}, nil
}

func normalizeCommand(command string) string {
	cmd := strings.TrimSpace(strings.ToLower(command))
	if cmd == "" {
		return ""
	}
	if strings.HasPrefix(cmd, "/") {
		return cmd
	}
	return "/" + cmd
}

func commandPatch(command, args string) (dto.ThreadConfigPatch, error) {
	value := strings.TrimSpace(args)
	if value == "" {
		return dto.ThreadConfigPatch{}, errors.New("command args are required")
	}
	switch command {
	case "/model":
		return dto.ThreadConfigPatch{Model: &value}, nil
	case "/personality":
		return dto.ThreadConfigPatch{Personality: &value}, nil
	case "/approvals":
		return dto.ThreadConfigPatch{Approvals: &value}, nil
	default:
		return dto.ThreadConfigPatch{}, fmt.Errorf("unsupported command: %s", command)
	}
}
