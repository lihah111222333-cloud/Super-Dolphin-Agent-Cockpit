package memory

import (
	"context"
	"errors"
)

type RootManager struct {
	svc Service
}

func NewRootManager(svc Service) *RootManager {
	return &RootManager{svc: svc}
}

func (m *RootManager) RootDir() string {
	if m == nil || m.svc == nil {
		return ""
	}
	return m.svc.RootDir()
}

func (m *RootManager) EnsureRoot(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return errors.New("memory service is nil")
	}
	return m.svc.EnsureRoot(ctx)
}
