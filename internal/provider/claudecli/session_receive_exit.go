package claudecli

import (
	"errors"
	"io"
)

func (s *session) handleReceiveExit(tr *transport, err error) {
	finishErr := err
	if finishErr == nil || errors.Is(finishErr, io.EOF) {
		finishErr = io.EOF
	}

	s.mu.Lock()
	if s.transport != tr {
		s.mu.Unlock()
		return
	}
	handle := s.takeActiveTurnLocked()
	s.mu.Unlock()

	s.finishTurnWithError(handle, finishErr)
}
