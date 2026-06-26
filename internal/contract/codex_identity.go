package contract

// Codex identity contract 被 cron、thread、provider routing、dashboard insight 和通知路径共同使用。
// 输入键、规范化流程、哨兵错误和输出字段是跨模块 wire 边界，变更时必须同步所有消费者。
import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CodexIdentity 是定位 codex app-server 实例的不可变三元组。
// Home、InstanceKey、ModelProvider 共同决定 codex thread 绑定到哪个本地进程，并持久化到 agent_provider_binding 供恢复使用。
// Home 必须是 CanonicalizeCodexHome 产出的真实路径，同一物理目录的不同写法要收敛成同一个值。
type CodexIdentity struct {
	Home          string
	InstanceKey   string
	ModelProvider string
}

// Codex identity 配置键名，必须与 runtime config wire 字段保持一致。
const (
	CodexHomeKey          = "codexHome"
	CodexInstanceKeyKey   = "codexInstanceKey"
	CodexModelProviderKey = "codexModelProvider"
)

const (
	RuntimeModeEnv       = "SUPER_DOLPHIN_RUNTIME_MODE"
	RuntimeModeDev       = "dev"
	RuntimeModePackaged  = "packaged"
	SuperDolphinHomeEnv  = "SUPER_DOLPHIN_HOME"
	CodexProviderHomeDir = "codex"
)

// Codex identity 解析错误哨兵；RPC 层用 errors.Is 映射到 InvalidParams。
var (
	ErrCodexHomeRequired          = errors.New("codexHome is required")
	ErrCodexInstanceKeyRequired   = errors.New("codexInstanceKey is required")
	ErrCodexModelProviderRequired = errors.New("codexModelProvider is required")
	ErrCodexHomeNotFound          = errors.New("codexHome directory does not exist")
	ErrCodexIdentityInvalidType   = errors.New("codex identity field has invalid type or value")
)

// ResolveCodexIdentity 从 runtime config 中解析 CodexIdentity。
// 三个字段都必须是非空字符串；Home 必须已存在并能规范化。本函数不创建目录、不使用默认 home。
func ResolveCodexIdentity(config map[string]any) (CodexIdentity, error) {
	home, err := requireCodexString(config, CodexHomeKey, ErrCodexHomeRequired)
	if err != nil {
		return CodexIdentity{}, err
	}
	key, err := requireCodexString(config, CodexInstanceKeyKey, ErrCodexInstanceKeyRequired)
	if err != nil {
		return CodexIdentity{}, err
	}
	provider, err := requireCodexString(config, CodexModelProviderKey, ErrCodexModelProviderRequired)
	if err != nil {
		return CodexIdentity{}, err
	}
	canonical, err := CanonicalizeCodexHome(home)
	if err != nil {
		return CodexIdentity{}, err
	}
	return CodexIdentity{
		Home:          canonical,
		InstanceKey:   key,
		ModelProvider: provider,
	}, nil
}

// CanonicalizeCodexHome 执行 codexHome 规范化流程。
// 它会展开 ~ 和环境变量、清理路径并解析 symlink；结果必须是已存在的绝对真实路径。
// 调用方应持久化该真实路径，而不是原始用户输入。
func CanonicalizeCodexHome(raw string) (string, error) {
	expanded, err := expandCodexHome(raw)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(expanded)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: codexHome must be absolute after expansion, got %q", ErrCodexIdentityInvalidType, cleaned)
	}
	real, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrCodexHomeNotFound, cleaned)
		}
		return "", fmt.Errorf("codexHome canonicalize: %w", err)
	}
	return real, nil
}

// requireCodexString 读取必需的 codex identity 字符串字段。
// 缺失、nil、非字符串或 trim 后为空都会返回稳定哨兵错误，避免下游猜测默认值。
func requireCodexString(config map[string]any, key string, missingErr error) (string, error) {
	raw, ok := config[key]
	if !ok || raw == nil {
		return "", missingErr
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: %q must be string, got %T", ErrCodexIdentityInvalidType, key, raw)
	}
	if s = strings.TrimSpace(s); s == "" {
		return "", missingErr
	}
	return s, nil
}

// expandCodexHome 展开 codexHome 中允许的 ~ 和环境变量。
// 明确拒绝 ~user 形式，避免调用方通过用户名访问其他用户 home。
func expandCodexHome(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrCodexHomeRequired
	}
	if strings.HasPrefix(s, "~") {
		switch {
		case s == "~":
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("codexHome ~ expand: %w", err)
			}
			s = home
		case strings.HasPrefix(s, "~/"):
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("codexHome ~ expand: %w", err)
			}
			s = filepath.Join(home, s[2:])
		default:
			// ~user/... 会让调用方按用户名寻址其他 home，身份解析边界明确拒绝。
			return "", fmt.Errorf("%w: ~user/... form not supported, got %q", ErrCodexIdentityInvalidType, raw)
		}
	}
	return os.ExpandEnv(s), nil
}

// RuntimeModeFromEnv 读取 runtime resolver 写入的运行模式。
// 空值表示当前进程没有声明 packaged 能力，非法值立即报错。
func RuntimeModeFromEnv() (string, error) {
	mode := strings.TrimSpace(os.Getenv(RuntimeModeEnv))
	switch mode {
	case "":
		return "", nil
	case RuntimeModeDev, RuntimeModePackaged:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid %s %q", RuntimeModeEnv, mode)
	}
}

// PackagedRuntimeFromEnv 判断当前 runtime 是否声明为 packaged 模式。
func PackagedRuntimeFromEnv() (bool, error) {
	mode, err := RuntimeModeFromEnv()
	if err != nil {
		return false, err
	}
	return mode == RuntimeModePackaged, nil
}

// CanonicalAppManagedCodexHome 返回 app 管理的 codex home 真实路径。
// 它复用 CanonicalizeCodexHome，因此目录不存在或路径非法都会 fail-fast。
func CanonicalAppManagedCodexHome() (string, error) {
	raw, err := AppManagedCodexHome()
	if err != nil {
		return "", err
	}
	return CanonicalizeCodexHome(raw)
}

// AppManagedCodexHome 根据 SUPER_DOLPHIN_HOME 计算 app 管理的 codex home。
// SUPER_DOLPHIN_HOME 必须是绝对路径；本函数只计算路径，不创建目录。
func AppManagedCodexHome() (string, error) {
	base := strings.TrimSpace(os.Getenv(SuperDolphinHomeEnv))
	if base == "" {
		return "", fmt.Errorf("%s is required for app-managed codex home", SuperDolphinHomeEnv)
	}
	base = filepath.Clean(os.ExpandEnv(base))
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("%s must be absolute: %s", SuperDolphinHomeEnv, base)
	}
	return filepath.Clean(filepath.Join(base, "providers", CodexProviderHomeDir)), nil
}
