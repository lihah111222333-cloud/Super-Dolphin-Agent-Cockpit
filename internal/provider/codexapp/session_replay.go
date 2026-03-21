package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type turnReplayState struct {
	localID    string
	providerID string
	params     turnStartParams
	handle     *turnHandle
}

func cloneTurnStartParams(params turnStartParams) turnStartParams {
	cloned := params
	if len(params.Input) > 0 {
		cloned.Input = append([]turnInputItem(nil), params.Input...)
	}
	if len(params.SelectedSkills) > 0 {
		cloned.SelectedSkills = append([]string(nil), params.SelectedSkills...)
	}
	if len(params.OutputSchema) > 0 {
		cloned.OutputSchema = append(json.RawMessage(nil), params.OutputSchema...)
	}
	return cloned
}

func (s *session) rememberPendingTurn(handle *turnHandle, params turnStartParams) {
	if s == nil || handle == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTurn = &turnReplayState{
		localID:    strings.TrimSpace(handle.LocalID()),
		providerID: strings.TrimSpace(handle.ProviderID()),
		params:     cloneTurnStartParams(params),
		handle:     handle,
	}
}

func (s *session) replayPendingTurn(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.pendingTurn == nil || s.pendingTurn.handle == nil {
		s.mu.Unlock()
		return nil
	}
	snapshot := *s.pendingTurn
	snapshot.params = cloneTurnStartParams(snapshot.params)
	s.mu.Unlock()
	select {
	case <-snapshot.handle.Done():
		return nil
	default:
	}
	if strings.TrimSpace(snapshot.params.ThreadID) == "" {
		return errors.New("codexapp: replay thread id is required")
	}
	if s.logger != nil {
		s.logger.Info("codexapp: replaying unfinished turn after recovery",
			"thread_id", snapshot.params.ThreadID,
			"local_turn_id", snapshot.localID,
			"provider_turn_id", snapshot.providerID,
		)
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := s.transport.Call(callCtx, "turn/start", snapshot.params)
	if err != nil {
		return err
	}
	var resp turnRPCResult
	if err := json.Unmarshal(raw, &resp); err != nil || strings.TrimSpace(resp.Turn.ID) == "" {
		return errors.New("codexapp: invalid turn/start response")
	}
	newProviderID := strings.TrimSpace(resp.Turn.ID)
	snapshot.handle.setProviderID(newProviderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.providerID != "" {
		delete(s.turns, snapshot.providerID)
	}
	s.turns[newProviderID] = snapshot.handle
	s.activeTurnID = newProviderID
	if s.pendingTurn != nil && s.pendingTurn.handle == snapshot.handle {
		s.pendingTurn.providerID = newProviderID
		s.pendingTurn.params = cloneTurnStartParams(snapshot.params)
	}
	if s.logger != nil {
		s.logger.Info("codexapp: unfinished turn replayed after recovery",
			"thread_id", snapshot.params.ThreadID,
			"local_turn_id", snapshot.localID,
			"old_provider_turn_id", snapshot.providerID,
			"new_provider_turn_id", newProviderID,
			"replayed_at", time.Now().UTC().Format(time.RFC3339Nano),
		)
	}
	return nil
}
