package acpnode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ProtocolVersion   = 1
	DefaultMaxMessage = 1 << 20
	DefaultMaxStderr  = 1 << 20
	MaxJSONDepth      = 64
	MaxMembers        = 256
	MaxPending        = 64
	MaxSessions       = 16
	MaxReverse        = 16
	MaxUpdates        = 4096
)

// LaunchConfig is the complete process boundary for the isolated ACP node.
// Env is the exact environment passed to the child; ambient process variables
// are never inherited.
type LaunchConfig struct {
	Enabled         bool
	Executable      string
	CWD             string
	Args            []string
	Env             []string
	EnvAllowlist    []string
	StartupTimeout  time.Duration
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	MaxMessage      int
	MaxStderr       int
}

// Validate 严格校验启用开关、子进程边界、环境白名单和资源上限。
func (c LaunchConfig) Validate() error {
	if err := validateConfigProvenance(); err != nil {
		return err
	}
	if err := c.validateEnabled(); err != nil {
		return err
	}
	if err := c.validateExecutable(); err != nil {
		return err
	}
	if err := c.validateCWD(); err != nil {
		return err
	}
	if err := c.validateEnvironment(); err != nil {
		return err
	}
	if err := c.validateArgs(); err != nil {
		return err
	}
	return c.validateBounds()
}

func (c LaunchConfig) validateEnabled() error {
	if !c.Enabled {
		return fmt.Errorf("acp: launch disabled")
	}
	return nil
}

// validateExecutable 确认可执行文件是绝对路径且具备普通文件执行权限。
func (c LaunchConfig) validateExecutable() error {
	if c.Executable == "" || !filepath.IsAbs(c.Executable) || strings.ContainsAny(c.Executable, "\x00\r\n") {
		return fmt.Errorf("acp: executable must be an absolute safe path")
	}
	info, err := os.Stat(c.Executable)
	if err != nil {
		return fmt.Errorf("acp: executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("acp: executable must be a regular executable file")
	}
	return nil
}

// validateCWD 确认子进程工作目录已存在且是绝对目录。
func (c LaunchConfig) validateCWD() error {
	if c.CWD == "" || !filepath.IsAbs(c.CWD) || strings.ContainsAny(c.CWD, "\x00\r\n") {
		return fmt.Errorf("acp: cwd must be an absolute safe path")
	}
	info, err := os.Stat(c.CWD)
	if err != nil {
		return fmt.Errorf("acp: cwd: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("acp: cwd must be an existing directory")
	}
	return nil
}

func (c LaunchConfig) validateEnvironment() error {
	if len(c.Env) == 0 {
		return fmt.Errorf("acp: explicit env is required")
	}
	allow, err := validateEnvAllowlist(c.EnvAllowlist)
	if err != nil {
		return err
	}
	return validateEnvEntries(c.Env, allow)
}

// validateEnvAllowlist 构造不含重复项的显式环境键白名单。
func validateEnvAllowlist(keys []string) (map[string]struct{}, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("acp: explicit env allowlist is required")
	}
	allow := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !validEnvKey(key) {
			return nil, fmt.Errorf("acp: invalid env allowlist key %q", key)
		}
		if _, exists := allow[key]; exists {
			return nil, fmt.Errorf("acp: duplicate env allowlist key %q", key)
		}
		allow[key] = struct{}{}
	}
	return allow, nil
}

// validateEnvEntries 拒绝未授权、重复或含控制字符的环境条目。
func validateEnvEntries(entries []string, allow map[string]struct{}) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !validEnvKey(key) || strings.ContainsAny(entry, "\x00\r\n") {
			return fmt.Errorf("acp: invalid env entry")
		}
		if _, ok := allow[key]; !ok {
			return fmt.Errorf("acp: env key %q is not allowlisted", key)
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("acp: duplicate env key %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (c LaunchConfig) validateArgs() error {
	for _, arg := range c.Args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("acp: argv contains NUL")
		}
	}
	return nil
}

// validateBounds 固定启动、请求、关闭和消息大小的正向边界。
func (c LaunchConfig) validateBounds() error {
	for _, timeout := range []struct {
		name  string
		value time.Duration
	}{{"startup", c.StartupTimeout}, {"request", c.RequestTimeout}, {"shutdown", c.ShutdownTimeout}} {
		if timeout.value <= 0 {
			return fmt.Errorf("acp: %s timeout must be positive", timeout.name)
		}
	}
	if c.MaxMessage <= 0 || c.MaxMessage > 8<<20 || c.MaxStderr <= 0 || c.MaxStderr > 8<<20 {
		return fmt.Errorf("acp: message/stderr bounds invalid")
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if !validEnvRune(i, r) {
			return false
		}
	}
	return true
}

// validEnvRune 判断环境键字符是否符合 POSIX 风格键名约束。
func validEnvRune(index int, r rune) bool {
	if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
		return true
	}
	return index > 0 && r >= '0' && r <= '9'
}
