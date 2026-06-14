package contract

// Phase 0 shared contract: ResolveCodexIdentity is consumed by cron, thread,
// provider routing, dashboard insight, and notification flows. Do not make
// breaking changes to the input keys, canonicalization pipeline, sentinel
// errors, or output fields without an ADR and coordinated downstream updates.
import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CodexIdentity is the immutable triple that identifies a codex app-server
// instance. All three fields together determine which local process a codex
// thread resolves to, and are persisted on agent_provider_binding for
// auto-resume.
//
// Home is always a canonicalized realpath produced by CanonicalizeCodexHome
// (home/env expansion, filepath.Clean, filepath.EvalSymlinks). Two inputs that
// point at the same physical directory (including symlink aliases) must produce
// the same Home.
type CodexIdentity struct {
	Home          string
	InstanceKey   string
	ModelProvider string
}

// Config keys consumed by ResolveCodexIdentity.
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

// Sentinel errors for codex identity resolution. Callers must check with
// errors.Is; RPC layers map these to jrpc2.InvalidParams.
var (
	ErrCodexHomeRequired          = errors.New("codexHome is required")
	ErrCodexInstanceKeyRequired   = errors.New("codexInstanceKey is required")
	ErrCodexModelProviderRequired = errors.New("codexModelProvider is required")
	ErrCodexHomeNotFound          = errors.New("codexHome directory does not exist")
	ErrCodexIdentityInvalidType   = errors.New("codex identity field has invalid type or value")
)

// ResolveCodexIdentity extracts the (Home, InstanceKey, ModelProvider) triple
// from a config map. All three keys must be present as non-empty strings.
//
// Home is canonicalized via CanonicalizeCodexHome. The directory must already
// exist; missing directories return ErrCodexHomeNotFound. This function does
// not create directories and does not fall back to a default home.
// ResolveCodexIdentity 解析codex身份。
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

// CanonicalizeCodexHome performs the full codex home canonicalization pipeline:
// ~ expansion, $ENV expansion, filepath.Clean, filepath.EvalSymlinks. The
// resulting path must be absolute and must exist. Callers should persist this
// realpath to binding rather than the raw user input.
// CanonicalizeCodexHome 处理canonicalizecodexhome。
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

// expandCodexHome 处理expandcodexhome。
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
			// ~user/... form not supported: would let a caller address another
			// user's home by name, which we explicitly refuse.
			return "", fmt.Errorf("%w: ~user/... form not supported, got %q", ErrCodexIdentityInvalidType, raw)
		}
	}
	return os.ExpandEnv(s), nil
}

// RuntimeModeFromEnv consumes the runtime-mode contract produced by the runtime
// resolver. Empty means no packaged capability has been advertised.
// RuntimeModeFromEnv 从env处理运行时模式。
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

// PackagedRuntimeFromEnv 从env处理packaged运行时。
func PackagedRuntimeFromEnv() (bool, error) {
	mode, err := RuntimeModeFromEnv()
	if err != nil {
		return false, err
	}
	return mode == RuntimeModePackaged, nil
}

// CanonicalAppManagedCodexHome 处理canonicalappmanagedcodexhome。
func CanonicalAppManagedCodexHome() (string, error) {
	raw, err := AppManagedCodexHome()
	if err != nil {
		return "", err
	}
	return CanonicalizeCodexHome(raw)
}

// AppManagedCodexHome 处理appmanagedcodexhome。
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
