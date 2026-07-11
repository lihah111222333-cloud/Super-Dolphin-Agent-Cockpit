package team

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
)

const teamSyncStateFileName = ".team-sync-state.json"

// SyncState 是团队记忆同步写在本地根目录内的持久状态。
// checksum/ETag 用于并发保护和差异比较，ServerMaxEntries 记住远端容量限制以拆分后续 push。
type SyncState struct {
	LastKnownChecksum string            `json:"lastKnownChecksum,omitempty"`
	ServerChecksums   map[string]string `json:"serverChecksums,omitempty"`
	ServerMaxEntries  int               `json:"serverMaxEntries,omitempty"`
	ServerETag        string            `json:"serverEtag,omitempty"`
}

// teamSyncStateStore 只负责读写 .team-sync-state.json；路径在构造时清理，后续调用不再接受外部文件名。
type teamSyncStateStore struct {
	path string
}

// newTeamSyncStateStore 校验团队记忆根目录并绑定状态文件路径，root 非绝对或不可清理时直接阻断同步初始化。
func newTeamSyncStateStore(root string) (*teamSyncStateStore, error) {
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return nil, err
	}
	return &teamSyncStateStore{path: filepath.Join(cleaned, teamSyncStateFileName)}, nil
}

// Load 读取本地同步状态；文件不存在代表首次同步，解析失败必须返回以避免用坏状态覆盖远端。
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

// Save 原子写入同步状态；状态为空时删除文件，避免留下误导后续拉取/推送的陈旧 ETag。
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

// Clear 删除同步状态文件；文件已不存在视为幂等成功，其他 I/O 错误继续上抛。
func (s *teamSyncStateStore) Clear() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// empty 判断状态是否没有可持久化字段，供 Save 决定是否清理状态文件。
func (s SyncState) empty() bool {
	return strings.TrimSpace(s.LastKnownChecksum) == "" &&
		len(s.ServerChecksums) == 0 &&
		s.ServerMaxEntries <= 0 &&
		strings.TrimSpace(s.ServerETag) == ""
}

// cloneSyncState 深拷贝同步状态中的 map，并清理字符串字段，避免调用方共享可变 map。
func cloneSyncState(state SyncState) SyncState {
	return SyncState{
		LastKnownChecksum: strings.TrimSpace(state.LastKnownChecksum),
		ServerChecksums:   cloneChecksumMap(state.ServerChecksums),
		ServerMaxEntries:  state.ServerMaxEntries,
		ServerETag:        strings.TrimSpace(state.ServerETag),
	}
}

// normalizeSyncState 规范化持久状态，负容量上限按未知处理，空 checksum map 收敛为 nil。
func normalizeSyncState(state SyncState) SyncState {
	state = cloneSyncState(state)
	if state.ServerMaxEntries < 0 {
		state.ServerMaxEntries = 0
	}
	return state
}

// cloneChecksumMap 复制并清理 checksum map，丢弃空路径或空 checksum，防止状态文件保存无效项。
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
