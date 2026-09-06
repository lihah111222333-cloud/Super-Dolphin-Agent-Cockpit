package standalone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestStandaloneBootstrapFiles(t *testing.T) {
	t.Run("credential", testCredentialFile)
	t.Run("ready_is_complete_and_non_overwriting", testReadyPublication)
	t.Run("ready_takeover_requires_lock_and_preserves_live_owner", testReadyTakeover)
}

func testCredentialFile(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential")
	valid := strings.Repeat("a", 32)
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{valid, true}, {valid + "\n", true}, {"", false}, {"short", false},
		{valid + " secret", false}, {strings.Repeat("x", 4097), false},
	} {
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := securefs.RestrictPrivateOwnerOnly(path, 0o600); err != nil {
			t.Fatal(err)
		}
		token, err := ReadCredential(path)
		if (err == nil) != tc.valid {
			t.Fatalf("credential validity mismatch: valid=%v err=%v", tc.valid, err)
		}
		if tc.valid && token != valid {
			t.Fatal("credential content changed")
		}
	}
	if _, err := ReadCredential(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing credential accepted")
	}
}

func testReadyPublication(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ready.json")
	expected := Ready{Endpoint: "http://127.0.0.1:3000/mcp", InstanceID: "test-instance"}
	cleanup, err := PublishReady(path, expected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	assertReadyContent(t, path, expected)
	if _, err := PublishReady(path, Ready{Endpoint: expected.Endpoint, InstanceID: "foreign"}); err == nil {
		t.Fatal("existing ready file overwritten")
	}
	assertReadyContent(t, path, expected)
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func assertReadyContent(t *testing.T, path string, expected Ready) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Ready
	if err := json.Unmarshal(raw, &got); err != nil || got != expected {
		t.Fatalf("ready = %#v, err=%v", got, err)
	}
}

func testReadyTakeover(t *testing.T) {
	t.Helper()
	readyDir := privateStateDir(t)
	path := filepath.Join(readyDir, "ready.json")
	stale := Ready{Endpoint: "http://127.0.0.1:1/mcp", InstanceID: "stale"}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	assertTakeoverRequiresLock(t, path)
	lock := acquireTestInstance(t, privateStateDir(t))
	replacement := Ready{Endpoint: "http://127.0.0.1:2/mcp", InstanceID: "new"}
	cleanup, err := PublishReadyAfterInstanceLock(lock, path, replacement)
	if err != nil {
		t.Fatal(err)
	}
	assertReadyContent(t, path, replacement)
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}

	liveState := privateStateDir(t)
	liveLock := acquireTestInstance(t, liveState)
	liveCleanup, err := PublishReadyAfterInstanceLock(liveLock, path, Ready{Endpoint: "http://127.0.0.1:3/mcp", InstanceID: "live"})
	if err != nil {
		t.Fatal(err)
	}
	contender := acquireTestInstance(t, privateStateDir(t))
	assertLiveReadyPreserved(t, contender, path)
	if err := liveCleanup(); err != nil {
		t.Fatal(err)
	}
	cleanup, err = PublishReadyAfterInstanceLock(contender, path, Ready{Endpoint: "http://127.0.0.1:4/mcp", InstanceID: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func assertTakeoverRequiresLock(t *testing.T, path string) {
	t.Helper()
	if _, err := PublishReadyAfterInstanceLock(nil, path, Ready{Endpoint: "http://127.0.0.1:2/mcp", InstanceID: "missing-lock"}); err == nil {
		t.Fatal("takeover without instance lock succeeded")
	}
}

func assertLiveReadyPreserved(t *testing.T, contender *InstanceLock, path string) {
	t.Helper()
	if _, err := PublishReadyAfterInstanceLock(contender, path, Ready{Endpoint: "http://127.0.0.1:4/mcp", InstanceID: "intruder"}); err == nil {
		t.Fatal("live ready publisher was overwritten")
	}
}

// TestReclaimStaleReadyIdentityRace 验证 ready 目录项在检查后被替换时，接管
// 只能隔离并恢复新 inode，不能把替换后的发现文件误删。
func TestReclaimStaleReadyIdentityRace(t *testing.T) {
	dir := privateStateDir(t)
	path := filepath.Join(dir, "ready.json")
	stale := Ready{Endpoint: "http://127.0.0.1:1/mcp", InstanceID: "stale"}
	replacement := Ready{Endpoint: "http://127.0.0.1:2/mcp", InstanceID: "replacement"}
	staleRaw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	replacementRaw, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, staleRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	movedStale := path + ".moved-by-test"
	defer os.Remove(movedStale)
	tracingRename := func(from, to string) error {
		if from == path {
			if err := os.Rename(from, movedStale); err != nil {
				return err
			}
			if err := os.WriteFile(path, replacementRaw, 0o600); err != nil {
				return err
			}
		}
		return os.Rename(from, to)
	}
	err = reclaimStaleReadyWithRename(path, tracingRename)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("reclaim race error = %v, want identity changed", err)
	}
	assertReadyContent(t, path, replacement)
	if _, err := os.Stat(movedStale); err != nil {
		t.Fatalf("original stale inode was not preserved for test cleanup: %v", err)
	}
}

// TestRemoveOwnedReadyIdentityRace 验证 cleanup 在目录项被替换时不会删除新 inode。
func TestRemoveOwnedReadyIdentityRace(t *testing.T) {
	dir := privateStateDir(t)
	path := filepath.Join(dir, "ready.json")
	owned := Ready{Endpoint: "http://127.0.0.1:1/mcp", InstanceID: "owned"}
	replacement := Ready{Endpoint: "http://127.0.0.1:2/mcp", InstanceID: "replacement"}
	ownedRaw, err := json.Marshal(owned)
	if err != nil {
		t.Fatal(err)
	}
	replacementRaw, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ownedRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	ownedInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	movedOwned := path + ".moved-by-test"
	defer os.Remove(movedOwned)
	tracingRename := func(from, to string) error {
		if from == path {
			if err := os.Rename(from, movedOwned); err != nil {
				return err
			}
			if err := os.WriteFile(path, replacementRaw, 0o600); err != nil {
				return err
			}
		}
		return os.Rename(from, to)
	}
	assertOwnedReadyRace(t, path, ownedInfo, tracingRename, replacement, movedOwned)
}

func assertOwnedReadyRace(t *testing.T, path string, ownedInfo os.FileInfo, renameFn func(string, string) error, replacement Ready, movedOwned string) {
	t.Helper()
	err := removeOwnedReadyWithRename(path, ownedInfo, renameFn)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("owned cleanup race error = %v, want identity changed", err)
	}
	assertReadyContent(t, path, replacement)
	if _, err := os.Stat(movedOwned); err != nil {
		t.Fatalf("owned inode was not preserved for test cleanup: %v", err)
	}
}
