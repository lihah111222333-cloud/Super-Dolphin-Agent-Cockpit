package skilllibrary

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store 是 library 物理目录的读写 API。
// 不做并发保护——调用方（reconcile / event handler）应序列化调用。
type Store struct {
	root string
}

func NewStore(root string) *Store { return &Store{root: root} }

// Install 写入 SKILL.md + sidecar 到 <root>/<name>/。
// 使用 .tmp + rename 模式实现原子覆盖；同名条目原子替换。
// 空 name 返回错误。
func (s *Store) Install(name string, skillMD []byte, meta SkillMeta) error {
	if name == "" {
		return fmt.Errorf("skilllibrary: install empty name")
	}
	dir := filepath.Join(s.root, name)
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("skilllibrary: cleanup tmp: %w", err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("skilllibrary: mkdir tmp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), skillMD, 0o644); err != nil {
		return fmt.Errorf("skilllibrary: write SKILL.md: %w", err)
	}
	if err := WriteMeta(tmp, meta); err != nil {
		return fmt.Errorf("skilllibrary: write meta: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("skilllibrary: remove old: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("skilllibrary: rename tmp: %w", err)
	}
	return nil
}

// Uninstall 删除整个 skill 目录；不存在视为成功（idempotent）。
// 空 name 返回错误。
func (s *Store) Uninstall(name string) error {
	if name == "" {
		return fmt.Errorf("skilllibrary: uninstall empty name")
	}
	return os.RemoveAll(filepath.Join(s.root, name))
}

// Get 读单个 skill；返回 fs.ErrNotExist 表示 skill 不存在。
// 空 name 返回验证错误，与 Install / Uninstall 保持一致。
func (s *Store) Get(name string) (*SkillEntry, error) {
	if name == "" {
		return nil, fmt.Errorf("skilllibrary: get empty name")
	}
	dir := filepath.Join(s.root, name)
	skillBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	meta, err := ReadMeta(dir)
	if err != nil {
		return nil, err
	}
	return &SkillEntry{Dir: dir, SkillMD: string(skillBytes), Meta: meta}, nil
}

// List 是 Scan 的便捷封装。
func (s *Store) List() ([]SkillEntry, error) { return Scan(s.root) }
