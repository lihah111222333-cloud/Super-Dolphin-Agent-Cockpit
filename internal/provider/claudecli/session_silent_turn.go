package claudecli

import (
	"context"
	"errors"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const keepaliveTimeout = 30 * time.Second

func (s *session) SendKeepalive(ctx context.Context) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	payload, localID, handle, err := s.prepareSilentTurnLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.transport.Send(payload); err != nil {
		s.failSilentTurnSendLocked(localID, handle, err)
		return err
	}
	s.mu.Unlock()

	timer := time.NewTimer(keepaliveTimeout)
	defer timer.Stop()

	select {
	case <-handle.Done():
		return handle.Err()
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
	if s.silentTurnIDs == nil {
		s.silentTurnIDs = map[string]struct{}{}
	}
	s.silentTurnIDs[localID] = struct{}{}
	return payload, localID, handle, nil
}

func (s *session) failSilentTurnSendLocked(localID string, handle *turnHandle, err error) {
	s.takeActiveTurnLocked()
	delete(s.silentTurnIDs, localID)
	s.mu.Unlock()
	handle.finish(err)
}

func (s *session) timeoutSilentTurn(localID string) error {
	s.mu.Lock()
	if s.transport != nil {
		_ = s.transport.Kill()
	}
	handle := s.takeActiveTurnLocked()
	delete(s.silentTurnIDs, localID)
	s.mu.Unlock()
	if handle != nil {
		handle.finish(errors.New("claudecli: keepalive timeout, killed transport"))
	}
	return errors.New("claudecli: keepalive timeout")
}

func (s *session) isSilentTurn(raw dto.RawProviderEvent) bool {
	turnID := dataString(raw.Data, "turn_id")

	s.mu.Lock()
	defer s.mu.Unlock()

	if turnID == "" {
		return false
	}
	_, silent := s.silentTurnIDs[turnID]
	return silent
}

func (s *session) finishSilentTurn(raw dto.RawProviderEvent) {
	turnID := dataString(raw.Data, "turn_id")
	if turnID == "" {
		return
	}

	handle := s.takeActiveTurn(turnID)
	s.mu.Lock()
	delete(s.silentTurnIDs, turnID)
	s.mu.Unlock()
	if handle == nil {
		return
	}
	if raw.EventType == "turn:interrupted" {
		handle.finish(context.Canceled)
		return
	}
	if dataBool(raw.Data, "success") {
		handle.finish(nil)
		return
	}
	handle.finish(errors.New(dataString(raw.Data, "error")))
}
