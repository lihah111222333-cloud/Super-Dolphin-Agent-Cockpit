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
	isSilent := false
	if handle != nil {
		_, isSilent = s.silentTurnIDs[handle.LocalID()]
		if isSilent {
			delete(s.silentTurnIDs, handle.LocalID())
		}
	}
	s.mu.Unlock()

	if isSilent {
		if handle != nil {
			handle.finish(finishErr)
		}
		return
	}
	s.finishTurnWithError(handle, finishErr)
}
