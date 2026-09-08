package codexwire

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type ownerCleanupFixture struct {
	calls         []string
	cleanErr      error
	cleanResult   any
	archiveErr    error
	cleanParams   map[string]string
	archiveParams map[string]string
}

// newOwnerCleanupFixture 使用真实RPC通道验证资源清理与thread卸载顺序。
func newOwnerCleanupFixture(t *testing.T) (*Process, *ownerCleanupFixture) {
	t.Helper()
	fixture := &ownerCleanupFixture{cleanResult: map[string]any{}}
	local := jrpcserver.NewLocal(handler.Map{
		"thread/backgroundTerminals/clean": handler.New(func(_ context.Context, input map[string]string) (any, error) {
			fixture.calls = append(fixture.calls, "clean")
			fixture.cleanParams = input
			if input["threadId"] != "thread" {
				return nil, errors.New("wrong cleanup identity")
			}
			return fixture.cleanResult, fixture.cleanErr
		}),
		"thread/archive": handler.New(func(_ context.Context, input map[string]string) (any, error) {
			fixture.calls = append(fixture.calls, "archive")
			fixture.archiveParams = input
			if input["threadId"] != "thread" {
				return nil, errors.New("wrong archive identity")
			}
			return map[string]any{}, fixture.archiveErr
		}),
	}, nil)
	t.Cleanup(func() {
		if err := local.Close(); err != nil {
			t.Error(err)
		}
	})
	return &Process{Client: local.Client}, fixture
}

// TestNativeArchiveCleansBeforeUnload 验证归档前清理和归档身份标记；错误不能被标记成成功。
func TestNativeArchiveCleansBeforeUnload(t *testing.T) {
	process, fixture := newOwnerCleanupFixture(t)
	if err := process.ArchiveThread(context.Background(), "thread"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.calls, []string{"clean", "archive"}) || process.archivedThreadID != "thread" {
		t.Fatalf("archive calls=%v identity=%s", fixture.calls, process.archivedThreadID)
	}
	for _, stage := range []string{"clean", "archive", "null-clean"} {
		t.Run(stage, func(t *testing.T) { assertNativeArchiveFailure(t, stage) })
	}
}

// TestCloseUsesOwnerCleanupBeforeProcessExit 验证Close在wire可用时先清理，已归档thread不再访问，失败仍回收进程并返回错误。
func TestCloseUsesOwnerCleanupBeforeProcessExit(t *testing.T) {
	for _, mode := range []string{"active", "archived", "cleanup-error"} {
		t.Run(mode, func(t *testing.T) {
			process, fixture := newOwnerCleanupFixture(t)
			attachOwnerCleanupProcess(t, process)
			if mode == "archived" {
				process.archivedThreadID = "thread"
			}
			if mode == "cleanup-error" {
				fixture.cleanErr = errors.New("cleanup unavailable")
			}
			err := process.Close()
			if (mode == "cleanup-error") != (err != nil) {
				t.Fatalf("Close error=%v mode=%s", err, mode)
			}
			if process.Running() {
				t.Fatal("Close left owned provider alive")
			}
			want := []string{"clean"}
			if mode == "archived" {
				want = nil
			}
			if !reflect.DeepEqual(fixture.calls, want) {
				t.Fatalf("Close calls=%v want=%v", fixture.calls, want)
			}
		})
	}
}

// attachOwnerCleanupProcess 给RPC fixture绑定真实可等待子进程，验证关停不是仅模拟Client调用。
func attachOwnerCleanupProcess(t *testing.T, process *Process) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestOwnerCleanupProcessFixture$")
	cmd.Env = append(os.Environ(), "ORCH_OWNER_CLEANUP_FIXTURE=1")
	processctl.Configure(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process.cmd, process.guard, process.done = cmd, processctl.Attach(cmd, nil), make(chan struct{})
	process.exitBind = contract.CLIProcessExitBinding{SessionID: "thread"}
	process.workers.Go(func() {
		err := cmd.Wait()
		process.exitMu.Lock()
		process.exitErr = err
		process.exitMu.Unlock()
		close(process.done)
	})
	t.Cleanup(func() {
		if process.Running() {
			if err := process.Close(); err != nil && !strings.Contains(err.Error(), "cleanup unavailable") {
				t.Error(err)
			}
		}
	})
}

// TestOwnerCleanupProcessFixture 只作为当前测试持有的子进程，不执行用户工具或模型。
func TestOwnerCleanupProcessFixture(t *testing.T) {
	if os.Getenv("ORCH_OWNER_CLEANUP_FIXTURE") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

// TestInitializeEnablesNativeCleanupCapability 验证原生owner清理所需的实验协议能力实际送入initialize。
func TestInitializeEnablesNativeCleanupCapability(t *testing.T) {
	enabled := false
	local := jrpcserver.NewLocal(handler.Map{
		"initialize": handler.New(func(_ context.Context, input struct {
			Capabilities struct {
				ExperimentalAPI bool `json:"experimentalApi"`
			} `json:"capabilities"`
		}) (map[string]any, error) {
			enabled = input.Capabilities.ExperimentalAPI
			return map[string]any{"userAgent": "fixture"}, nil
		}),
		"initialized": handler.New(func(context.Context, map[string]any) (map[string]any, error) { return map[string]any{}, nil }),
	}, nil)
	t.Cleanup(func() {
		if err := local.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := initialize(context.Background(), local.Client); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("initialize omitted native cleanup protocol capability")
	}
}

// TestNativeThreadIdentityFieldCoverage 动态枚举私有RPC身份DTO，验证clean/archive两个实际消费方向均完整映射。
func TestNativeThreadIdentityFieldCoverage(t *testing.T) {
	process, fixture := newOwnerCleanupFixture(t)
	if err := process.ArchiveThread(context.Background(), "thread"); err != nil {
		t.Fatal(err)
	}
	producer := reflect.TypeOf(nativeThreadIdentity{})
	for _, params := range []map[string]string{fixture.cleanParams, fixture.archiveParams} {
		if len(params) != producer.NumField() {
			t.Fatalf("native identity field count drift: %v", params)
		}
		for i := 0; i < producer.NumField(); i++ {
			key := strings.Split(producer.Field(i).Tag.Get("json"), ",")[0]
			if key != "threadId" || params[key] != "thread" {
				t.Fatalf("unmapped native identity field %s: %v", key, params)
			}
		}
	}
}

// assertNativeArchiveFailure 保留原生清理和归档每个失败阶段的严格断言。
func assertNativeArchiveFailure(t *testing.T, stage string) {
	t.Helper()
	process, fixture := newOwnerCleanupFixture(t)
	switch stage {
	case "clean":
		fixture.cleanErr = errors.New("cleanup unavailable")
	case "archive":
		fixture.archiveErr = errors.New("archive unavailable")
	case "null-clean":
		fixture.cleanResult = nil
	}
	if err := process.ArchiveThread(context.Background(), "thread"); err == nil {
		t.Fatal("owner error swallowed")
	}
	if process.archivedThreadID != "" {
		t.Fatal("failed archive recorded as completed")
	}
	if stage != "archive" && len(fixture.calls) != 1 {
		t.Fatal("archive executed after cleanup failure")
	}
}
