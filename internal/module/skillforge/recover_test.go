package skillforge

import (
	"os"
	"path/filepath"
	"testing"
)

// recover_test.go: 验证 spec §4.4 startup recovery 行为。
// 残骸命名约定与 atomic.go 一致：
//   - tmp:    .{name}.tmp-{pid}-{ns}
//   - backup: .{name}.bak-{pid}-{ns}
//   - legacy: {name}.tmp（pre-Gap#1 老格式，不带前导点）

func TestRecoverStaging_RestoresBackupWhenTargetMissing(t *testing.T) {
	cache := t.TempDir()
	bak := filepath.Join(cache, ".foo.bak-1-1")
	if err := os.MkdirAll(bak, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bak, "SKILL.md"), []byte("v-old"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RecoverStaging(cache)
	if err != nil {
		t.Fatalf("RecoverStaging: %v", err)
	}
	if len(report.Restored) != 1 || report.Restored[0] != "foo" {
		t.Fatalf("Restored = %v, want [foo]", report.Restored)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Errorf("backup not cleaned: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cache, "foo", "SKILL.md"))
	if err != nil || string(b) != "v-old" {
		t.Fatalf("target not restored: content=%q err=%v", string(b), err)
	}
}

func TestRecoverStaging_DiscardsBackupWhenTargetExists(t *testing.T) {
	cache := t.TempDir()
	target := filepath.Join(cache, "foo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("v-new"), 0o644); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(cache, ".foo.bak-1-1")
	if err := os.MkdirAll(bak, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bak, "SKILL.md"), []byte("v-old"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RecoverStaging(cache)
	if err != nil {
		t.Fatalf("RecoverStaging: %v", err)
	}
	if len(report.RemovedBackup) != 1 || report.RemovedBackup[0] != "foo" {
		t.Fatalf("RemovedBackup = %v, want [foo]", report.RemovedBackup)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Errorf("backup not removed: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(b) != "v-new" {
		t.Errorf("target overwritten by stale backup: %q", string(b))
	}
}

func TestRecoverStaging_RemovesStaleTmp(t *testing.T) {
	cache := t.TempDir()
	tmp := filepath.Join(cache, ".foo.tmp-1-1")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "junk"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RecoverStaging(cache)
	if err != nil {
		t.Fatalf("RecoverStaging: %v", err)
	}
	if len(report.RemovedTmp) != 1 {
		t.Fatalf("RemovedTmp = %v, want 1 entry", report.RemovedTmp)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("stale tmp not removed: %v", err)
	}
}

func TestRecoverStaging_RemovesLegacyTmp(t *testing.T) {
	cache := t.TempDir()
	legacy := filepath.Join(cache, "bar.tmp")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := RecoverStaging(cache)
	if err != nil {
		t.Fatalf("RecoverStaging: %v", err)
	}
	if len(report.RemovedTmp) != 1 {
		t.Fatalf("RemovedTmp = %v, want 1 legacy entry", report.RemovedTmp)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy tmp not removed: %v", err)
	}
}

func TestRecoverStaging_PreservesValidSkillAndUnknownFiles(t *testing.T) {
	cache := t.TempDir()
	skill := filepath.Join(cache, "foo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 未知普通目录不应被 recovery 触动；属于 reconcile.removeOrphans 范畴
	other := filepath.Join(cache, "unrelated")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := RecoverStaging(cache)
	if err != nil {
		t.Fatalf("RecoverStaging: %v", err)
	}
	if len(report.Restored)+len(report.RemovedBackup)+len(report.RemovedTmp) != 0 {
		t.Errorf("recovery touched non-staging entries: %+v", report)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("valid skill removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("unrelated dir removed: %v", err)
	}
}

func TestRecoverStaging_MissingCacheDirReturnsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	report, err := RecoverStaging(missing)
	if err != nil {
		t.Fatalf("RecoverStaging on missing dir: %v", err)
	}
	if report == nil {
		t.Fatal("report is nil")
	}
	if len(report.Restored)+len(report.RemovedBackup)+len(report.RemovedTmp)+len(report.Errors) != 0 {
		t.Errorf("expected empty report, got %+v", report)
	}
}

func TestRecoverStaging_IgnoresNonDirEntriesNamedLikeStaging(t *testing.T) {
	// .{name}.bak-{suffix} 必须是目录才作为 backup 处理；同名普通文件不应被
	// 误删，避免对外部用户文件造成意外破坏。
	cache := t.TempDir()
	weird := filepath.Join(cache, ".foo.bak-1-1")
	if err := os.WriteFile(weird, []byte("not a backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := RecoverStaging(cache)
	if err != nil {
		t.Fatalf("RecoverStaging: %v", err)
	}
	if len(report.Restored)+len(report.RemovedBackup) != 0 {
		t.Errorf("non-dir staging-like entry should be ignored: %+v", report)
	}
	if _, err := os.Stat(weird); err != nil {
		t.Errorf("non-dir staging-like file removed: %v", err)
	}
}
