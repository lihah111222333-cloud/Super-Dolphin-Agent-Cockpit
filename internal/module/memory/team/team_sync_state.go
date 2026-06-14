package team

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

const teamSyncStateFileName = ".team-sync-state.json"

type SyncState struct {
	LastKnownChecksum string            `json:"lastKnownChecksum,omitempty"`
	ServerChecksums   map[string]string `json:"serverChecksums,omitempty"`
	ServerMaxEntries  int               `json:"serverMaxEntries,omitempty"`
	ServerETag        string            `json:"serverEtag,omitempty"`
}

type teamSyncStateStore struct {
	path string
}

func newTeamSyncStateStore(root string) (*teamSyncStateStore, error) {
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return nil, err
	}
	return &teamSyncStateStore{path: filepath.Join(cleaned, teamSyncStateFileName)}, nil
}

// Load 加载记忆。
func (s *teamSyncStateStore) Load() (SyncState, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return SyncState{}, nil
	}
	data, err := os.ReadFile(s.path)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return SyncState{}, nil
	default:
		return SyncState{}, err
	}
	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return SyncState{}, err
	}
	return normalizeSyncState(state), nil
}

// Save 保存记忆。
func (s *teamSyncStateStore) Save(state SyncState) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	state = normalizeSyncState(state)
	if state.empty() {
		return s.Clear()
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

// Clear 清理记忆。
func (s *teamSyncStateStore) Clear() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s SyncState) empty() bool {
	return strings.TrimSpace(s.LastKnownChecksum) == "" &&
		len(s.ServerChecksums) == 0 &&
		s.ServerMaxEntries <= 0 &&
		strings.TrimSpace(s.ServerETag) == ""
}

func cloneSyncState(state SyncState) SyncState {
	return SyncState{
		LastKnownChecksum: strings.TrimSpace(state.LastKnownChecksum),
		ServerChecksums:   cloneChecksumMap(state.ServerChecksums),
		ServerMaxEntries:  state.ServerMaxEntries,
		ServerETag:        strings.TrimSpace(state.ServerETag),
	}
}

func normalizeSyncState(state SyncState) SyncState {
	state = cloneSyncState(state)
	if state.ServerMaxEntries < 0 {
		state.ServerMaxEntries = 0
	}
	return state
}

// cloneChecksumMap 复制checksummap。
func cloneChecksumMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(filepath.ToSlash(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		cloned[key] = value
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}
