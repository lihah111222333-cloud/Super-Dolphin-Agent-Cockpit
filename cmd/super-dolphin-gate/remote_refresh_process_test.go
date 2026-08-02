package main

import (
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

func TestNewDetachedRemoteBaselineRefreshCommandUsesWorkerExecutable(t *testing.T) {
	executable := "/tmp/super-dolphin-gate"
	args := []string{"_remote-baseline-refresh-worker", "--ledger", "/tmp/ledger.sqlite"}
	command, err := newDetachedRemoteBaselineRefreshCommand(executable, args...)
	if err != nil {
		t.Fatalf("new detached command: %v", err)
	}

	if command.Path != executable {
		t.Fatalf("command path = %q, want worker executable %q", command.Path, executable)
	}
	if !slices.Equal(command.Args, append([]string{executable}, args...)) {
		t.Fatalf("command args = %#v, want %#v", command.Args, append([]string{executable}, args...))
	}
}

func TestConfigureDetachedRemoteBaselineRefreshCommandUsesPlatformIsolation(t *testing.T) {
	command := exec.Command("worker")
	err := configureDetachedRemoteBaselineRefreshCommand(command)

	switch runtime.GOOS {
	case "darwin", "linux":
		if err != nil {
			t.Fatalf("configure detached command: %v", err)
		}
		if command.SysProcAttr == nil || !reflect.ValueOf(command.SysProcAttr).Elem().FieldByName("Setsid").Bool() {
			t.Fatal("Setsid must isolate the detached refresh worker")
		}
	case "windows":
		if err != nil {
			t.Fatalf("configure detached command: %v", err)
		}
		attributes := reflect.ValueOf(command.SysProcAttr).Elem()
		if command.SysProcAttr == nil || attributes.FieldByName("CreationFlags").Uint() != 0x08000200 || !attributes.FieldByName("HideWindow").Bool() {
			t.Fatal("Windows detached refresh worker must have process-group and hidden-window flags")
		}
	default:
		if err == nil {
			t.Fatalf("configure detached command on unsupported %s succeeded", runtime.GOOS)
		}
	}
}

func TestRemoteBaselineRefreshWorkerEnvReplacesInheritedToken(t *testing.T) {
	parent := []string{
		"PATH=/bin",
		remoteBaselineRefreshTokenEnv + "=stale-token",
		"OTHER=value",
		remoteBaselineRefreshTokenEnv + "=older-token",
		remoteBaselineRefreshTokenEnv + "_SUFFIX=preserved",
	}

	environment := remoteBaselineRefreshWorkerEnv(parent, "current-token")
	want := []string{
		"PATH=/bin",
		"OTHER=value",
		remoteBaselineRefreshTokenEnv + "_SUFFIX=preserved",
		remoteBaselineRefreshTokenEnv + "=current-token",
	}
	if !slices.Equal(environment, want) {
		t.Fatalf("worker environment = %#v, want %#v", environment, want)
	}
}
