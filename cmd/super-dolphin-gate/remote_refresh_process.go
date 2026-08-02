package main

import (
	"fmt"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"syscall"
)

// newDetachedRemoteBaselineRefreshCommand 创建不依赖外部 shell 或会话工具的后台刷新命令。
func newDetachedRemoteBaselineRefreshCommand(executable string, args ...string) (*exec.Cmd, error) {
	command := exec.Command(executable, args...)
	if err := configureDetachedRemoteBaselineRefreshCommand(command); err != nil {
		return nil, err
	}
	return command, nil
}

// configureDetachedRemoteBaselineRefreshCommand 为受支持平台的 worker 配置独立进程组。
func configureDetachedRemoteBaselineRefreshCommand(command *exec.Cmd) error {
	if command == nil {
		return fmt.Errorf("detached remote baseline refresh command is required")
	}

	attributes := reflect.New(reflect.TypeOf(syscall.SysProcAttr{})).Elem()
	switch runtime.GOOS {
	case "darwin", "linux":
		if err := setSysProcAttrField(attributes, "Setsid", true); err != nil {
			return err
		}
	case "windows":
		const creationFlags = uint32(0x00000200 | 0x08000000) // CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
		if err := setSysProcAttrField(attributes, "CreationFlags", creationFlags); err != nil {
			return err
		}
		if err := setSysProcAttrField(attributes, "HideWindow", true); err != nil {
			return err
		}
	default:
		return fmt.Errorf("detached remote baseline refresh is unsupported on %s", runtime.GOOS)
	}

	command.SysProcAttr = attributes.Addr().Interface().(*syscall.SysProcAttr)
	return nil
}

func setSysProcAttrField(attributes reflect.Value, name string, value any) error {
	field := attributes.FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		return fmt.Errorf("syscall.SysProcAttr.%s is unavailable on %s", name, runtime.GOOS)
	}
	configured := reflect.ValueOf(value)
	if !configured.Type().AssignableTo(field.Type()) {
		return fmt.Errorf("syscall.SysProcAttr.%s has unexpected type %s on %s", name, field.Type(), runtime.GOOS)
	}
	field.Set(configured)
	return nil
}

// remoteBaselineRefreshWorkerEnv 使用当前 lease token 覆盖继承环境中的旧 token。
func remoteBaselineRefreshWorkerEnv(parent []string, token string) []string {
	prefix := remoteBaselineRefreshTokenEnv + "="
	environment := make([]string, 0, len(parent)+1)
	for _, entry := range parent {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, prefix+token)
}
