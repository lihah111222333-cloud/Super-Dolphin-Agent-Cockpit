//go:build e2e

package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

type standaloneManagedProcessSnapshot []pidregistry.StableProcessIdentity
type standaloneIdentityReader func(int) (pidregistry.StableProcessIdentity, error)

// captureStandaloneManagedProcesses 固定关停前已观测的后代身份，拒绝没有提供方身份的证据。
func captureStandaloneManagedProcesses(entries []standaloneProcessEntry, executable string, read standaloneIdentityReader) (standaloneManagedProcessSnapshot, error) {
	observed := make(standaloneManagedProcessSnapshot, 0, len(entries))
	providerObserved := false
	for _, entry := range entries {
		identity, err := read(entry.pid)
		if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("pid=%d identity_read_failed", entry.pid)
		}
		if identity.PID != entry.pid || identity.ProcessStartToken == "" || identity.ExecutableIdentity == "" {
			return nil, fmt.Errorf("pid=%d identity_invalid", entry.pid)
		}
		observed = append(observed, identity)
		if standaloneTreeContainsProvider([]standaloneProcessEntry{entry}, executable) {
			providerObserved = true
		}
	}
	if !providerObserved {
		return nil, errors.New("provider_identity_not_observed")
	}
	return observed, nil
}

func validStandaloneProcessIdentity(identity pidregistry.StableProcessIdentity) bool {
	return identity.PID > 0 && identity.ProcessStartToken != "" && identity.ExecutableIdentity != ""
}

// standaloneManagedProcessesAlive 不依赖 PPID；同一启动令牌即仍存活，读取失败不能成为退出证据。
func standaloneManagedProcessesAlive(observed standaloneManagedProcessSnapshot, read standaloneIdentityReader) ([]int, error) {
	if len(observed) == 0 {
		return nil, errors.New("empty_process_snapshot")
	}
	alive := make([]int, 0, len(observed))
	for _, original := range observed {
		if !validStandaloneProcessIdentity(original) {
			return nil, fmt.Errorf("pid=%d snapshot_identity_invalid", original.PID)
		}
		current, err := read(original.PID)
		if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("pid=%d identity_read_failed", original.PID)
		}
		if current.PID != original.PID || current.ProcessStartToken == "" {
			return nil, fmt.Errorf("pid=%d identity_invalid", original.PID)
		}
		if current.ProcessStartToken == original.ProcessStartToken {
			alive = append(alive, original.PID)
		}
	}
	return alive, nil
}

// TestStandaloneManagedProcessReapingEvidence 锁定重挂父进程、换映像、PID 复用及读取错误的证据边界。
func TestStandaloneManagedProcessReapingEvidence(t *testing.T) {
	original := pidregistry.StableProcessIdentity{PID: 42, ProcessStartToken: "start-1", ExecutableIdentity: "provider"}
	snapshot := standaloneManagedProcessSnapshot{original}
	t.Run("capture-evidence", testStandaloneManagedProcessCaptureEvidence)
	orphanTable := map[int]standaloneProcessEntry{42: {pid: 42, ppid: 1, command: "provider"}}
	if got := standaloneDescendants(orphanTable, 30); len(got) != 0 {
		t.Fatalf("controlled old-PPID counterexample returned %d descendants", len(got))
	}
	cases := []struct {
		name      string
		observed  standaloneManagedProcessSnapshot
		current   pidregistry.StableProcessIdentity
		err       error
		wantAlive bool
		wantErr   bool
	}{
		{name: "reparented-still-alive", observed: snapshot, current: original, wantAlive: true},
		{name: "same-start-token-new-executable", observed: snapshot, current: pidregistry.StableProcessIdentity{PID: 42, ProcessStartToken: "start-1", ExecutableIdentity: "new-image"}, wantAlive: true},
		{name: "not-found", observed: snapshot, err: pidregistry.ErrStableProcessNotFound},
		{name: "pid-reused", observed: snapshot, current: pidregistry.StableProcessIdentity{PID: 42, ProcessStartToken: "start-2", ExecutableIdentity: "provider"}},
		{name: "permission-denied", observed: snapshot, err: errors.New("permission denied"), wantErr: true},
		{name: "identity-read-error", observed: snapshot, err: pidregistry.ErrStableProcessIdentityRead, wantErr: true},
		{name: "empty-snapshot", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			reader := func(pid int) (pidregistry.StableProcessIdentity, error) {
				reads++
				if pid != original.PID {
					t.Fatalf("unexpected identity read pid=%d", pid)
				}
				return tc.current, tc.err
			}
			alive, err := standaloneManagedProcessesAlive(tc.observed, reader)
			if (err != nil) != tc.wantErr || (len(alive) > 0) != tc.wantAlive {
				t.Fatalf("alive=%v err=%v wantAlive=%t wantErr=%t", alive, err, tc.wantAlive, tc.wantErr)
			}
			if reads != len(tc.observed) {
				t.Fatalf("identity reads=%d want=%d", reads, len(tc.observed))
			}
		})
	}
}

// testStandaloneManagedProcessCaptureEvidence 验证扫描竞态只允许已退出的单项消失。
func testStandaloneManagedProcessCaptureEvidence(t *testing.T) {
	entries := []standaloneProcessEntry{{pid: 42, command: "/provider"}, {pid: 43, command: "worker"}}
	cases := []struct {
		name       string
		missingPID int
		readErr    error
		wantErr    bool
	}{
		{name: "all-observed"},
		{name: "exited-worker", missingPID: 43, readErr: pidregistry.ErrStableProcessNotFound},
		{name: "exited-provider", missingPID: 42, readErr: pidregistry.ErrStableProcessNotFound, wantErr: true},
		{name: "unreadable-worker", missingPID: 43, readErr: errors.New("permission denied"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := func(pid int) (pidregistry.StableProcessIdentity, error) {
				if pid == tc.missingPID {
					return pidregistry.StableProcessIdentity{}, tc.readErr
				}
				return pidregistry.StableProcessIdentity{PID: pid, ProcessStartToken: "start", ExecutableIdentity: "image"}, nil
			}
			observed, err := captureStandaloneManagedProcesses(entries, "/provider", reader)
			if (err != nil) != tc.wantErr {
				t.Fatalf("capture err=%v wantErr=%t", err, tc.wantErr)
			}
			if err == nil && len(observed) == 0 {
				t.Fatal("capture passed with empty snapshot")
			}
		})
	}
}
