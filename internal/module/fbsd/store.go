package fbsd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LoadStats 从 path 读 JSON。
//   - 文件不存在 → 空 Stats（不报错），便于首次运行
//   - 文件存在但 malformed → wrapped error
func LoadStats(path string) (Stats, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Stats{}, nil
		}
		return nil, fmt.Errorf("fbsd: read stats: %w", err)
	}
	var s Stats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("fbsd: parse stats: %w", err)
	}
	if s == nil {
		s = Stats{}
	}
	return s, nil
}

// SaveStats 把 stats 原子写到 path（mkdir + tmp + rename）。
// path 的父目录会被自动创建。stats 可为 nil（写空对象 {}）。
func SaveStats(path string, stats Stats) error {
	if path == "" {
		return fmt.Errorf("fbsd: SaveStats empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("fbsd: mkdir: %w", err)
	}
	if stats == nil {
		stats = Stats{}
	}
	body, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("fbsd: marshal stats: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("fbsd: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fbsd: rename: %w", err)
	}
	return nil
}
