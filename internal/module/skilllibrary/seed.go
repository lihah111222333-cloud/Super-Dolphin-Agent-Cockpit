package skilllibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// SeedBuiltins 把 //go:embed 的内置 skill 写入 library。
// 规则（spec §5.2）：
//   - 库里没有该 skill → 全新安装为 origin=builtin
//   - 库里有，origin=builtin，且 version_hash 不一致 → 覆盖（视为 harness 升级）
//   - 库里有，origin != builtin → 保留（用户已自定义版）
//
// 返回实际写入的 skill 数（跳过的不计入）。
func SeedBuiltins(store *Store, harnessVersion string, reader contract.EmbeddedSkillReader) (int, error) {
	names, err := reader.ListNames()
	if err != nil {
		return 0, fmt.Errorf("skilllibrary: list embedded: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	written := 0
	for _, name := range names {
		body, err := reader.Read(name)
		if err != nil {
			return written, fmt.Errorf("skilllibrary: read embedded %s: %w", name, err)
		}
		hash := sha256Hex(body)
		existing, err := store.Get(name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return written, fmt.Errorf("skilllibrary: get %s: %w", name, err)
		}
		if existing != nil {
			if existing.Meta.Origin != OriginBuiltin {
				continue // user-modified, skip
			}
			if existing.Meta.VersionHash == hash {
				continue // already up to date
			}
		}
		meta := SkillMeta{
			Name:        name,
			Origin:      OriginBuiltin,
			Version:     harnessVersion,
			VersionHash: hash,
			InstalledAt: now,
		}
		if err := store.Install(name, body, meta); err != nil {
			return written, fmt.Errorf("skilllibrary: seed install %s: %w", name, err)
		}
		written++
	}
	return written, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
