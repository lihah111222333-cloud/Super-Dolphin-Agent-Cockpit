package pidregistry

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)


func TestRegistryRegisterAndPersist(t *testing.T) {
	r := &Registry{
		appPID:   99999,
		path:     filepath.Join(t.TempDir(), "test-reg.json"),
		children: make(map[int]ChildInfo),
	}
	defer r.Close()

	firstPID := startIdentityTestProcess(t)
	secondPID := startIdentityTestProcess(t)
	r.Register(firstPID, "codex-app-server", nil)
	r.Register(secondPID, "claude-cli", map[string]string{"agent_id": "a1"})

	// Verify file was written.
	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read registry file: %v", err)
	}
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rf.AppPID != 99999 {
		t.Errorf("app_pid = %d, want 99999", rf.AppPID)
	}
	if len(rf.Children) != 2 {
		t.Errorf("children count = %d, want 2", len(rf.Children))
	}
}

func TestRegistryPersistWritesPrivateProvenance(t *testing.T) {
	r := &Registry{
		appPID:   99994,
		path:     filepath.Join(t.TempDir(), "test-reg.json"),
		children: make(map[int]ChildInfo),
	}
	defer r.Close()

	if err := r.RegisterChecked(os.Getpid(), "codex-app-server", nil); err != nil {
		t.Fatalf("RegisterChecked() error = %v", err)
	}

	info, err := os.Stat(r.path)
	if err != nil {
		t.Fatalf("stat registry file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("registry file mode = %03o, want 600", got)
	}

	var raw map[string]any
	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read registry file: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal registry JSON: %v", err)
	}
	for _, key := range []string{"nonce", "created_at", "parent_executable_fingerprint"} {
		value, ok := raw[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			t.Fatalf("registry JSON missing non-empty %q: %#v", key, raw)
		}
	}
}

func TestRegistryRegisterCheckedReturnsPersistError(t *testing.T) {
	tmpBlocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmpBlocker, []byte("block registry path"), 0o600); err != nil {
		t.Fatalf("write tmp blocker: %v", err)
	}
	r := &Registry{
		appPID:   99995,
		path:     filepath.Join(tmpBlocker, "test-reg.json"),
		children: make(map[int]ChildInfo),
	}

	err := r.RegisterChecked(os.Getpid(), "codex-app-server", nil)
	if err == nil {
		t.Fatal("RegisterChecked() error = nil, want persist failure")
	}
	if !strings.Contains(err.Error(), "pidregistry") {
		t.Fatalf("RegisterChecked() error = %v, want pidregistry context", err)
	}
	if len(r.children) != 0 {
		t.Fatalf("children count = %d, want rollback after persist failure", len(r.children))
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := &Registry{
		appPID:   99998,
		path:     filepath.Join(t.TempDir(), "test-unreg.json"),
		children: make(map[int]ChildInfo),
	}
	defer r.Close()

	firstPID := startIdentityTestProcess(t)
	secondPID := startIdentityTestProcess(t)
	r.Register(firstPID, "codex-app-server", nil)
	r.Register(secondPID, "claude-cli", nil)
	r.Unregister(firstPID)

	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rf.Children) != 1 {
		t.Errorf("children count = %d, want 1", len(rf.Children))
	}
	if rf.Children[0].PID != secondPID {
		t.Errorf("remaining pid = %d, want %d", rf.Children[0].PID, secondPID)
	}
}

func TestRegistryClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-close.json")
	r := &Registry{
		appPID:   99997,
		path:     path,
		children: make(map[int]ChildInfo),
	}
	r.Register(os.Getpid(), "test", nil)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist before close: %v", err)
	}
	r.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed after close")
	}
}

func startIdentityTestProcess(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperSleepProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_SLEEP_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start identity test process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestHelperSleepProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_SLEEP_PROCESS") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}


func TestRegistryNilSafe(t *testing.T) {
	var r *Registry
	// None of these should panic.
	r.Register(1234, "test", nil)
	r.Unregister(1234)
	r.Close()
}

func TestRegistrySkipsInvalidPID(t *testing.T) {
	r := &Registry{
		appPID:   99996,
		path:     filepath.Join(t.TempDir(), "test-invalid.json"),
		children: make(map[int]ChildInfo),
	}
	defer r.Close()

	r.Register(0, "test", nil)
	r.Register(1, "test", nil)
	r.Register(-1, "test", nil)

	r.mu.Lock()
	count := len(r.children)
	r.mu.Unlock()
	if count != 0 {
		t.Errorf("should skip invalid PIDs, got %d children", count)
	}
}

func TestParsePIDFromFilename(t *testing.T) {
	tests := []struct {
		name    string
		wantPID int
		wantOK  bool
	}{
		{"super-agent-pids-12345.json", 12345, true},
		{"super-agent-pids-1.json", 1, true},
		{"super-agent-pids-0.json", 0, false},
		{"super-agent-pids-.json", 0, false},
		{"unrelated-file.json", 0, false},
		{"super-agent-pids-abc.json", 0, false},
	}
	for _, tt := range tests {
		pid, ok := ParsePIDFromFilename(tt.name)
		if ok != tt.wantOK || pid != tt.wantPID {
			t.Errorf("ParsePIDFromFilename(%q) = (%d, %v), want (%d, %v)",
				tt.name, pid, ok, tt.wantPID, tt.wantOK)
		}
	}
}

func TestFindStaleRegistryFiles(t *testing.T) {
	// Create a fake stale registry file with a dead PID.
	deadPID := 99999999 // very unlikely to be a real PID
	path := registryPath(deadPID)
	rf := registryFile{
		AppPID:                      deadPID,
		Nonce:                       "test-nonce",
		CreatedAt:                   "2026-07-06T00:00:00Z",
		ParentExecutableFingerprint: "sha256:test",
		Children: []ChildInfo{
			{PID: 88888888, Kind: "test"},
		},
	}
	data, _ := json.Marshal(rf)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	defer os.Remove(path)

	stale := findStaleRegistryFiles()
	found := false
	for _, sf := range stale {
		if sf.AppPID == deadPID {
			found = true
			break
		}
	}
	if !found {
		t.Error("should find stale file for dead PID")
	}
}

func TestFindStaleSkipsLivePID(t *testing.T) {
	// Our own PID should never appear as stale.
	myPID := os.Getpid()
	path := registryPath(myPID)
	rf := registryFile{
		AppPID: myPID,
		Children: []ChildInfo{
			{PID: 88888888, Kind: "test"},
		},
	}
	data, _ := json.Marshal(rf)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	defer os.Remove(path)

	stale := findStaleRegistryFiles()
	for _, sf := range stale {
		if sf.AppPID == myPID {
			t.Error("should not include our own PID as stale")
		}
	}
}

func TestRegistryPath(t *testing.T) {
	path := registryPath(12345)
	expected := filepath.Join(registryDir(), filePrefix+strconv.Itoa(12345)+fileSuffix)
	if path != expected {
		t.Errorf("registryPath(12345) = %q, want %q", path, expected)
	}
}

func TestCollectStaleOrphansSkipsProtectedPIDs(t *testing.T) {
	myPID := os.Getpid()
	staleFiles := []staleFile{{
		registryFile: registryFile{
			AppPID: 99999999,
			Children: []ChildInfo{
				{PID: myPID, Kind: "codex-app-server"},
				{PID: 99999998, Kind: "dead-child"},
			},
		},
	}}
	got, _ := collectStaleOrphans(staleFiles, map[int]struct{}{myPID: {}})
	for _, orphan := range got {
		if orphan.child.PID == myPID {
			t.Fatalf("collectStaleOrphans() included protected current PID: %#v", got)
		}
	}
}
