package heterocinode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const windowsFactsScript = `$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';$computer=Get-CimInstance Win32_ComputerSystem;$os=Get-CimInstance Win32_OperatingSystem;[ordered]@{processor_count=[int64]$env:NUMBER_OF_PROCESSORS;total_memory_bytes=[int64]$computer.TotalPhysicalMemory;free_memory_bytes=[int64]$os.FreePhysicalMemory*1024;boot_id=$os.LastBootUpTime.ToUniversalTime().ToString('o')}|ConvertTo-Json -Compress`

// WindowsFactsProviderConfig 显式绑定 PowerShell executable 与进程等待边界。
type WindowsFactsProviderConfig struct {
	PowerShellExecutable string
	Timeout              time.Duration
	WaitDelay            time.Duration
}

// WindowsFactsProvider 通过固定 CIM 脚本采集 Windows native 节点事实。
type WindowsFactsProvider struct {
	runner windowsCommandRunner
}

type windowsCommandRunner interface {
	Run(context.Context, ...string) ([]byte, error)
}

// NewWindowsFactsProvider 构造不发现默认 PowerShell 路径的 Windows facts provider。
func NewWindowsFactsProvider(config WindowsFactsProviderConfig) (*WindowsFactsProvider, error) {
	if err := validateWindowsFactsProviderConfig(config); err != nil {
		return nil, err
	}
	return &WindowsFactsProvider{runner: windowsExecCommandRunner{
		executable: config.PowerShellExecutable, timeout: config.Timeout, waitDelay: config.WaitDelay,
	}}, nil
}

// Collect 采集 ProcessorCount、CIM 总/可用内存和 boot identity，并以逻辑处理器数约束节点并发。
func (provider *WindowsFactsProvider) Collect(ctx context.Context, config Config, _ heteroci.ProbeRequest) (Facts, error) {
	if provider == nil || provider.runner == nil || config.Node.Platform != heteroci.PlatformWindowsAMD64 {
		return Facts{}, errors.New("windows facts provider dependencies are invalid")
	}
	if len(config.Node.Capabilities) != 1 || config.Node.Capabilities[0].Runtime != heteroci.RuntimeWindowsNative {
		return Facts{}, errors.New("windows facts provider requires windows_native capability")
	}
	encodedScript := base64.StdEncoding.EncodeToString(utf16LEBytes(windowsFactsScript))
	output, err := provider.runner.Run(ctx, "-NoLogo", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", encodedScript)
	if err != nil {
		return Facts{}, errors.New("windows CIM facts query failed")
	}
	facts, err := decodeWindowsFacts(output)
	if err != nil {
		return Facts{}, err
	}
	// Windows 外层命令使用 UTF-16/base64 双层编码；八个受限 go test 可稳定低于 CreateProcess 32767 字符边界。
	maxConcurrency := int(min(facts.ProcessorCount, int64(8)))
	total := heteroci.ResourceCapacity{CPUMilli: facts.ProcessorCount * 1000, MemoryBytes: facts.TotalMemoryBytes, MaxConcurrency: maxConcurrency}
	available := heteroci.ResourceCapacity{CPUMilli: total.CPUMilli, MemoryBytes: facts.FreeMemoryBytes, MaxConcurrency: maxConcurrency}
	return Facts{
		BootID: facts.BootID, Total: total, Available: available,
		Capabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeWindowsNative}},
	}, nil
}

type windowsFactsDocument struct {
	ProcessorCount   int64  `json:"processor_count"`
	TotalMemoryBytes int64  `json:"total_memory_bytes"`
	FreeMemoryBytes  int64  `json:"free_memory_bytes"`
	BootID           string `json:"boot_id"`
}

// decodeWindowsFacts 严格解码 bounded CIM JSON 并校验 CPU、内存与 boot identity。
func decodeWindowsFacts(data []byte) (windowsFactsDocument, error) {
	if len(data) == 0 || len(data) > 64<<10 {
		return windowsFactsDocument{}, errors.New("windows facts output size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var facts windowsFactsDocument
	if err := decoder.Decode(&facts); err != nil || !windowsFactsEOF(decoder) {
		return windowsFactsDocument{}, errors.New("windows facts output is invalid")
	}
	if !validWindowsFactsCapacity(facts) || strings.TrimSpace(facts.BootID) == "" {
		return windowsFactsDocument{}, errors.New("windows facts values are invalid")
	}
	return facts, nil
}

// validWindowsFactsCapacity 校验 CIM CPU 与内存容量边界。
func validWindowsFactsCapacity(facts windowsFactsDocument) bool {
	return facts.ProcessorCount > 0 && facts.ProcessorCount <= 4_096 && facts.TotalMemoryBytes > 0 &&
		facts.FreeMemoryBytes > 0 && facts.FreeMemoryBytes <= facts.TotalMemoryBytes
}

func windowsFactsEOF(decoder *json.Decoder) bool {
	var trailing json.RawMessage
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

// validateWindowsFactsProviderConfig 校验 PowerShell 路径与进程等待边界。
func validateWindowsFactsProviderConfig(config WindowsFactsProviderConfig) error {
	if err := validateWindowsPowerShellExecutable(config.PowerShellExecutable); err != nil {
		return err
	}
	if config.Timeout < time.Second || config.Timeout > 120*time.Second || config.WaitDelay <= 0 || config.WaitDelay > config.Timeout {
		return errors.New("windows facts provider timing is invalid")
	}
	return nil
}

// validateWindowsPowerShellExecutable 拒绝相对、非 clean、symlink 或非普通 PowerShell 路径。
func validateWindowsPowerShellExecutable(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("windows PowerShell executable path is invalid")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("windows PowerShell executable path contains a symlink")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("windows PowerShell executable is invalid")
	}
	return nil
}

func utf16LEBytes(value string) []byte {
	units := utf16.Encode([]rune(value))
	encoded := make([]byte, len(units)*2)
	for index, unit := range units {
		encoded[index*2] = byte(unit)
		encoded[index*2+1] = byte(unit >> 8)
	}
	return encoded
}

type windowsExecCommandRunner struct {
	executable string
	timeout    time.Duration
	waitDelay  time.Duration
}

// Run 在 context timeout 与 WaitDelay 双重边界内调用唯一 PowerShell executable。
func (runner windowsExecCommandRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, runner.executable, args...)
	command.WaitDelay = runner.waitDelay
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || runCtx.Err() != nil || stdout.Len() > 64<<10 {
		return nil, errors.New("PowerShell command failed")
	}
	return slices.Clone(bytes.TrimSpace(stdout.Bytes())), nil
}
