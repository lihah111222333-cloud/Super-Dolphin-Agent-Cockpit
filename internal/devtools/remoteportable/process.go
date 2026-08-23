package remoteportable

import (
	"errors"
	"os/exec"
	"time"
)

const portableCommandWaitDelay = 5 * time.Second

// ConfigureBoundedCommand 为 portable Git command 配置跨平台取消和 pipe drain 上限。
func ConfigureBoundedCommand(command *exec.Cmd) error {
	if command == nil {
		return errors.New("portable bounded command is required")
	}
	command.WaitDelay = portableCommandWaitDelay
	if err := configurePortableProcess(command); err != nil {
		return err
	}
	command.Cancel = func() error {
		return cancelPortableProcess(command)
	}
	return nil
}
