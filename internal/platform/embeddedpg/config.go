package embeddedpg

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
)

const (
	EnvPostgresBinDir   = "SUPER_DOLPHIN_POSTGRES_BIN_DIR"
	EnvPostgresShareDir = "SUPER_DOLPHIN_POSTGRES_SHARE_DIR"
	EnvPostgresPort     = "SUPER_DOLPHIN_POSTGRES_PORT"
	EnvHome             = "SUPER_DOLPHIN_HOME"
	EnvProcessRole      = "SUPER_DOLPHIN_PROCESS_ROLE"
	EnvEmbeddedPostgres = "SUPER_DOLPHIN_EMBEDDED_POSTGRES"

	defaultDatabaseName = "super_dolphin"
	defaultUserName     = "super_dolphin"
	defaultPort         = 55432
)

type ResolveInput struct {
	GOOS           string
	GOARCH         string
	Env            map[string]string
	ExecutablePath string
	ProjectRoot    string
	UserHome       string
}

func ResolveFromEnvironment(projectRoot string) (contract.EmbeddedPostgresConfig, string) {
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	return ResolveConfig(ResolveInput{
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		Env:            environmentMap(os.Environ()),
		ExecutablePath: exe,
		ProjectRoot:    projectRoot,
		UserHome:       home,
	})
}

func ResolveConfig(input ResolveInput) (contract.EmbeddedPostgresConfig, string) {
	goos := firstNonEmpty(input.GOOS, runtime.GOOS)
	goarch := firstNonEmpty(input.GOARCH, runtime.GOARCH)
	env := input.Env
	if env == nil {
		env = map[string]string{}
	}
	if databaseURL := firstEnv(env, "DATABASE_URL", "POSTGRES_CONNECTION_STRING"); databaseURL != "" {
		return contract.EmbeddedPostgresConfig{}, databaseURL
	}
	if isSidecar(env) {
		return contract.EmbeddedPostgresConfig{}, ""
	}
	packagedRuntime, packaged := resolveInputPackagedRuntime(input, env, goos, goarch)
	if !embeddedPostgresRequested(env) {
		return contract.EmbeddedPostgresConfig{}, ""
	}
	base, resolveErr := appDataRoot(goos, env, input.UserHome)
	if packaged && strings.TrimSpace(env[EnvHome]) == "" {
		base = packagedRuntime.AppDataDir
	}
	port, portErr := resolvePort(env)
	resolveErr = joinResolveErrors(resolveErr, portErr)
	binDir := resolveBinDir(input, goos, goarch, packagedRuntime, packaged)
	cfg := contract.EmbeddedPostgresConfig{
		Enabled:               true,
		Owner:                 resolveOwner(env),
		RecoverRunningDataDir: packaged && resolveOwner(env),
		BinDir:                binDir,
		ShareDir:              resolveShareDir(env, binDir),
		DataDir:               filepath.Join(base, "postgres", "data"),
		RuntimeDir:            resolveRuntimeDir(base, goos, packaged, port),
		LogPath:               filepath.Join(base, "logs", "postgres.log"),
		DatabaseName:          defaultDatabaseName,
		UserName:              defaultUserName,
		Port:                  port,
		ResolveError:          resolveErr,
	}
	return cfg, databaseURLFor(cfg, goos)
}

func resolveInputPackagedRuntime(input ResolveInput, env map[string]string, goos, goarch string) (runtimeenv.PackagedRuntime, bool) {
	resolved, err := runtimeenv.ResolveRuntime(runtimeenv.RuntimeResolveInput{
		GOOS:           goos,
		GOARCH:         goarch,
		Env:            env,
		ExecutablePath: input.ExecutablePath,
		UserHome:       input.UserHome,
	})
	if err != nil || resolved.RuntimeMode != runtimeenv.RuntimeModePackaged || resolved.PackagedRuntime == nil {
		return runtimeenv.PackagedRuntime{}, false
	}
	return *resolved.PackagedRuntime, true
}

func resolveRuntimeDir(base, goos string, packaged bool, port int) string {
	if !packaged {
		return filepath.Join(base, "runtime", "postgres")
	}
	root := os.TempDir()
	if goos == "darwin" {
		root = "/tmp"
	}
	return filepath.Join(root, "sd-pg-"+strconv.Itoa(os.Getuid())+"-"+strconv.Itoa(port))
}

func environmentMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

func databaseURLFor(cfg contract.EmbeddedPostgresConfig, goos string) string {
	values := url.Values{}
	values.Set("sslmode", "disable")
	host := "localhost:" + strconv.Itoa(cfg.Port)
	if goos == "windows" {
		host = "127.0.0.1:" + strconv.Itoa(cfg.Port)
	} else {
		values.Set("host", cfg.RuntimeDir)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.User(cfg.UserName),
		Host:     host,
		Path:     "/" + cfg.DatabaseName,
		RawQuery: values.Encode(),
	}
	return u.String()
}

func appDataRoot(goos string, env map[string]string, userHome string) (string, string) {
	if home := strings.TrimSpace(env[EnvHome]); home != "" {
		return filepath.Clean(home), ""
	}
	userHome = strings.TrimSpace(userHome)
	if userHome == "" {
		return filepath.Join(".", ".super-dolphin"), "resolve embedded postgres home: user home is empty"
	}
	switch goos {
	case "darwin":
		return filepath.Join(userHome, "Library", "Application Support", "Super Dolphin"), ""
	case "linux":
		if xdg := strings.TrimSpace(env["XDG_DATA_HOME"]); xdg != "" {
			return filepath.Join(xdg, "super-dolphin"), ""
		}
		return filepath.Join(userHome, ".local", "share", "super-dolphin"), ""
	default:
		return filepath.Join(userHome, ".super-dolphin"), ""
	}
}

func resolveBinDir(input ResolveInput, goos, goarch string, packagedRuntime runtimeenv.PackagedRuntime, packaged bool) string {
	if explicit := strings.TrimSpace(input.Env[EnvPostgresBinDir]); explicit != "" {
		return filepath.Clean(explicit)
	}
	platform := goos + "-" + goarch
	if packaged {
		return filepath.Join(packagedRuntime.ResourcesDir, "postgres", platform, "bin")
	}
	if root := strings.TrimSpace(input.ProjectRoot); root != "" {
		return filepath.Join(root, "third_party", "postgres", platform, "bin")
	}
	if exe := strings.TrimSpace(input.ExecutablePath); exe != "" {
		exeDir := filepath.Dir(exe)
		if goos == "darwin" && filepath.Base(filepath.Dir(exeDir)) == "Contents" {
			appRoot := filepath.Dir(filepath.Dir(exeDir))
			return filepath.Join(appRoot, "Contents", "Resources", "postgres", platform, "bin")
		}
		return filepath.Join(exeDir, "postgres", platform, "bin")
	}
	return filepath.Join("third_party", "postgres", platform, "bin")
}

func resolveOwner(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[EnvProcessRole]), "desktop")
}

func isSidecar(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[EnvProcessRole]), "sidecar")
}

func embeddedPostgresRequested(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[EnvEmbeddedPostgres]), "true")
}

func resolveShareDir(env map[string]string, binDir string) string {
	if explicit := strings.TrimSpace(env[EnvPostgresShareDir]); explicit != "" {
		return filepath.Clean(explicit)
	}
	prefix := filepath.Dir(strings.TrimSpace(binDir))
	for _, candidate := range postgresShareDirCandidates(prefix) {
		if _, err := os.Stat(filepath.Join(candidate, "postgres.bki")); err == nil {
			return candidate
		}
	}
	return filepath.Join(prefix, "share", "postgresql@16")
}

func postgresShareDirCandidates(prefix string) []string {
	return []string{
		filepath.Join(prefix, "share"),
		filepath.Join(prefix, "share", "postgresql@16"),
		filepath.Join(prefix, "share", "postgresql"),
	}
}

func resolvePort(env map[string]string) (int, string) {
	raw := strings.TrimSpace(env[EnvPostgresPort])
	if raw == "" {
		return defaultPort, ""
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 || parsed > 65535 {
		return 0, "SUPER_DOLPHIN_POSTGRES_PORT must be an integer between 1 and 65535"
	}
	return parsed, ""
}

func firstEnv(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func joinResolveErrors(values ...string) string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, "; ")
}
