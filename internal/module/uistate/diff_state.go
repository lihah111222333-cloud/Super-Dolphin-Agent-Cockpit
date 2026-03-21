package uistate

import "context"

type diffStateRequest struct {
	threadID    string
	includeDiff bool
	known       int
}

type diffStateSnapshot struct {
	threadID string
	revision int64
}

type diffStateRequestKey struct{}

func withDiffStateRequest(ctx context.Context, threadID string, includeDiff bool, known int) context.Context {
	return context.WithValue(ctx, diffStateRequestKey{}, diffStateRequest{
		threadID:    threadID,
		includeDiff: includeDiff,
		known:       known,
	})
}

func diffStateRequestFromContext(ctx context.Context) diffStateRequest {
	if ctx == nil {
		return diffStateRequest{}
	}
	value, _ := ctx.Value(diffStateRequestKey{}).(diffStateRequest)
	return value
}

func (s *service) diffStateSnapshot(ctx context.Context) diffStateSnapshot {
	req := diffStateRequestFromContext(ctx)
	if !req.includeDiff || req.threadID == "" {
		return diffStateSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return diffStateSnapshot{
		threadID: req.threadID,
		revision: s.currentDiffRevisionLocked(req.threadID),
	}
}

func applyDiffStateSnapshot(ctx context.Context, snapshot *UIState, current diffStateSnapshot) {
	if snapshot == nil || current.threadID == "" {
		return
	}
	req := diffStateRequestFromContext(ctx)
	snapshot.DiffRevisionByThread = map[string]int64{current.threadID: current.revision}
	if req.known > 0 && int64(req.known) == current.revision {
		snapshot.DiffTextByThread = map[string]string{}
		snapshot.Unchanged = true
		return
	}
	snapshot.DiffTextByThread = map[string]string{current.threadID: ""}
	snapshot.Unchanged = false
}
