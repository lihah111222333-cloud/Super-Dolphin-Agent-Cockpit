package claudecli

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const keepaliveTimeout = 30 * time.Second

func (s *session) keepaliveLogger() *slog.Logger {
	if s != nil && s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

func (s *session) SendKeepalive(ctx context.Context) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}

	logger := s.keepaliveLogger()
	s.mu.Lock()
	payload, localID, handle, err := s.prepareSilentTurnLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	logger.Info("claudecli: keepalive sending", "local_id", localID)
	if err := s.transport.Send(payload); err != nil {
		s.failSilentTurnSendLocked(handle, err)
		return err
	}
	s.mu.Unlock()

	timer := time.NewTimer(keepaliveTimeout)
	defer timer.Stop()

	select {
	case <-handle.Done():
		err := handle.Err()
		if err == nil {
			logger.Info("claudecli: keepalive completed", "local_id", localID)
		}
		return err
	case <-timer.C:
		return s.timeoutSilentTurn(localID)
	}
}

func (s *session) prepareSilentTurnLocked() ([]byte, string, *turnHandle, error) {
	if err := ensureTurnAvailable(s.activeTurn); err != nil {
		return nil, "", nil, err
	}
	if s.transport == nil || !s.transport.readyForSend() {
		return nil, "", nil, errors.New("claudecli: transport not ready for keepalive")
	}

	payload, err := marshalTurnPayload(
		"[CACHE-KEEPALIVE] Automated cache maintenance. Reply with only: OK",
	)
	if err != nil {
		return nil, "", nil, err
	}

	localID := "keepalive_" + shared.NewID("ping")
	handle := newTurnHandle(localID, localID)
	s.activeTurn = handle
	return payload, localID, handle, nil
}

func (s *session) failSilentTurnSendLocked(handle *turnHandle, err error) {
	s.takeActiveTurnLocked()
	s.mu.Unlock()
	handle.finish(err)
}

func (s *session) timeoutSilentTurn(localID string) error {
	logger := s.keepaliveLogger()
	logger.Warn("claudecli: keepalive timeout, killing transport", "local_id", localID)
	s.mu.Lock()
	if s.transport != nil {
		_ = s.transport.Kill()
	}
	handle := s.takeActiveTurnLocked()
	s.mu.Unlock()
	if handle != nil {
		handle.finish(errors.New("claudecli: keepalive timeout, killed transport"))
	}
	return errors.New("claudecli: keepalive timeout")
}

