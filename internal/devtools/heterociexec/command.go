// Package heterociexec 提供异构 CI 执行器注册和严格命令编码端口。
package heterociexec

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const windowsCommandLineMaxUTF16CodeUnits = 32_766

// Command 是部署端可消费的严格命令表示；Unix 保留 Argv，Windows 额外携带 UTF-16LE base64。
type Command struct {
	Platform      heteroci.Platform
	Argv          []string
	CommandLine   string
	CommandBase64 string
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

// StrictCommandEncoder 实现 Linux、macOS 和 Windows 的唯一严格 argv 编码。
type StrictCommandEncoder struct{}

// Encode 校验 argv 后保留 Unix 参数边界，或生成 Windows UTF-16LE CommandBase64。
func (StrictCommandEncoder) Encode(platform heteroci.Platform, argv []string) (Command, error) {
	if err := validatePlatform(platform); err != nil {
		return Command{}, err
	}
	if err := validateArgv(argv); err != nil {
		return Command{}, err
	}
	encoded := Command{Platform: platform, Argv: slices.Clone(argv)}
	if platform == heteroci.PlatformWindowsAMD64 {
		encoded.CommandLine = joinWindowsCommandLine(argv)
		if len(utf16.Encode([]rune(encoded.CommandLine))) > windowsCommandLineMaxUTF16CodeUnits {
			return Command{}, fmt.Errorf("%w: Windows command line exceeds CreateProcess limit", ErrCommandEncoding)
		}
		encoded.CommandBase64 = base64.StdEncoding.EncodeToString(utf16LEBytes(encoded.CommandLine))
	}
	return encoded, nil
}

// EncodeCommand 使用严格内建编码器生成单个平台命令，供无 registry 的单元测试或端口调用。
func EncodeCommand(platform heteroci.Platform, argv []string) (Command, error) {
	return (StrictCommandEncoder{}).Encode(platform, argv)
}

// validatePlatform 只允许当前部署契约覆盖的 Linux、macOS 和 Windows 平台。
func validatePlatform(platform heteroci.Platform) error {
	switch platform {
	case heteroci.PlatformLinuxAMD64, heteroci.PlatformDarwinAMD64, heteroci.PlatformDarwinARM64, heteroci.PlatformWindowsAMD64:
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

// joinWindowsCommandLine 使用 CommandLineToArgvW 兼容规则保留每个参数边界。
func joinWindowsCommandLine(argv []string) string {
	parts := make([]string, len(argv))
	for index, argument := range argv {
		parts[index] = quoteWindowsArgument(argument)
	}
	return strings.Join(parts, " ")
}

// quoteWindowsArgument 对空格、引号和尾部反斜杠执行严格 Windows 命令行转义。
func quoteWindowsArgument(argument string) string {
	needsQuotes := argument == ""
	if !needsQuotes {
		for _, character := range argument {
			if unicode.IsSpace(character) || character == '"' {
				needsQuotes = true
				break
			}
		}
	}
	if !needsQuotes {
		return argument
	}

	var builder strings.Builder
	builder.Grow(len(argument) + 2)
	builder.WriteByte('"')
	backslashes := 0
	for _, character := range argument {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			builder.WriteString(strings.Repeat("\\", backslashes*2+1))
			builder.WriteByte('"')
			backslashes = 0
			continue
		}
		builder.WriteString(strings.Repeat("\\", backslashes))
		builder.WriteRune(character)
		backslashes = 0
	}
	builder.WriteString(strings.Repeat("\\", backslashes*2))
	builder.WriteByte('"')
	return builder.String()
}

// utf16LEBytes 将 Go 字符串编码为 PowerShell [Text.Encoding]::Unicode 的小端字节序。
func utf16LEBytes(value string) []byte {
	units := utf16.Encode([]rune(value))
	encoded := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	return encoded
}

// ErrCommandEncoding 表示命令边界、平台或编码协议不合法。
var ErrCommandEncoding = errors.New("heteroci executor command encoding failed")
