// Package heterociexec 提供异构 CI 执行器注册和严格命令编码端口。
package heterociexec

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// Command 是部署端可消费的严格命令表示；物理执行器只接受 Linux argv，Windows/amd64 仅作为 Wine logical target。
type Command struct {
	Platform heteroci.Platform
	Argv     []string
}

// CommandEncoder 将规范 argv 编码为目标平台的部署命令，不允许 shell fallback。
type CommandEncoder interface {
	Encode(platform heteroci.Platform, argv []string) (Command, error)
}

// CommandEncoderFunc 将函数适配为可注入的命令编码器。
type CommandEncoderFunc func(platform heteroci.Platform, argv []string) (Command, error)

// Encode 调用注入函数；nil 函数立即拒绝，避免隐式默认编码器。
func (function CommandEncoderFunc) Encode(platform heteroci.Platform, argv []string) (Command, error) {
	if function == nil {
		return Command{}, ErrCommandEncoding
	}
	return function(platform, argv)
}

// StrictCommandEncoder 实现 Linux physical host 上两个 logical target 的严格 argv 编码。
type StrictCommandEncoder struct{}

// Encode 校验 argv 并保留参数边界，不生成 physical-host shell 字符串编码。
func (StrictCommandEncoder) Encode(platform heteroci.Platform, argv []string) (Command, error) {
	if err := validatePlatform(platform); err != nil {
		return Command{}, err
	}
	if err := validateArgv(argv); err != nil {
		return Command{}, err
	}
	return Command{Platform: platform, Argv: slices.Clone(argv)}, nil
}

// EncodeCommand 使用严格内建编码器生成单个平台命令，供无 registry 的单元测试或端口调用。
func EncodeCommand(platform heteroci.Platform, argv []string) (Command, error) {
	return (StrictCommandEncoder{}).Encode(platform, argv)
}

// validatePlatform 只允许单 Ubuntu physical host 上的 Linux 与 Windows/Wine logical target。
func validatePlatform(platform heteroci.Platform) error {
	switch platform {
	case heteroci.PlatformLinuxAMD64, heteroci.PlatformWindowsAMD64:
		return nil
	default:
		return fmt.Errorf("%w: unsupported platform %q", ErrCommandEncoding, platform)
	}
}

// validateArgv 拒绝空命令、空可执行文件、NUL、非法 UTF-8 和不可传递的参数。
func validateArgv(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("%w: argv must not be empty", ErrCommandEncoding)
	}
	if argv[0] == "" {
		return fmt.Errorf("%w: executable argument must not be empty", ErrCommandEncoding)
	}
	for index, argument := range argv {
		if !utf8.ValidString(argument) {
			return fmt.Errorf("%w: argv[%d] is not valid UTF-8", ErrCommandEncoding, index)
		}
		if strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("%w: argv[%d] contains NUL", ErrCommandEncoding, index)
		}
	}
	return nil
}

// ErrCommandEncoding 表示命令边界、平台或编码协议不合法。
var ErrCommandEncoding = errors.New("heteroci executor command encoding failed")
