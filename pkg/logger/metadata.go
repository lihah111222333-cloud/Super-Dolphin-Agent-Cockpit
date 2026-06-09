package logger

import (
	"log/slog"
	"os"
	"strings"
)

const (
	serviceNameEnv    = "SUPER_DOLPHIN_SERVICE_NAME"
	serviceVersionEnv = "SUPER_DOLPHIN_SERVICE_VERSION"
	serviceEnvEnv     = "SUPER_DOLPHIN_ENV"
	appEnvEnv         = "APP_ENV"
	runtimeModeEnv    = "SUPER_DOLPHIN_RUNTIME_MODE"
	updateVersionEnv  = "SUPER_DOLPHIN_UPDATE_VERSION"
)

func ConfigureServiceFromEnv(defaultVersion string) {
	SetServiceMetadata(
		firstLogValue(os.Getenv(serviceNameEnv), "super-dolphin"),
		firstLogValue(os.Getenv(serviceVersionEnv), os.Getenv(updateVersionEnv), defaultVersion, "dev"),
		normalizeLogEnv(firstLogValue(os.Getenv(serviceEnvEnv), os.Getenv(appEnvEnv), os.Getenv(runtimeModeEnv), "dev")),
	)
}

func SetServiceMetadata(name, version, env string) {
	name = firstLogValue(name, "super-dolphin")
	version = firstLogValue(version, "dev")
	env = normalizeLogEnv(firstLogValue(env, "dev"))

	logFileMu.Lock()
	globalServiceName = name
	globalServiceVersion = version
	globalEnv = env
	logFileMu.Unlock()

	rebuildActiveLogger()
}

func applyGlobalAttrs(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return nil
	}
	attrs := currentGlobalAttrs()
	if len(attrs) == 0 {
		return logger
	}
	return logger.With(attrs...)
}

func currentGlobalAttrs() []any {
	logFileMu.Lock()
	project := strings.TrimSpace(globalProject)
	serviceName := strings.TrimSpace(globalServiceName)
	serviceVersion := strings.TrimSpace(globalServiceVersion)
	env := strings.TrimSpace(globalEnv)
	logFileMu.Unlock()

	attrs := make([]any, 0, 8)
	if serviceName != "" {
		attrs = append(attrs, FieldServiceName, serviceName)
	}
	if serviceVersion != "" {
		attrs = append(attrs, FieldServiceVersion, serviceVersion)
	}
	if env != "" {
		attrs = append(attrs, FieldEnv, env)
	}
	if project != "" {
		attrs = append(attrs, "project", project)
	}
	return attrs
}

func rebuildActiveLogger() {
	logFileMu.Lock()
	f := logFile
	mode := activeMode
	level := activeLevel
	logFileMu.Unlock()
	if f != nil {
		rebuildLoggerWithFile(f)
		return
	}
	storeLogger(newLogger(mode, level))
}

func normalizeLogEnv(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "dev", "development", "local", "debug":
		return "dev"
	case "test", "testing":
		return "test"
	case "prod", "production", "release", "packaged":
		return "prod"
	default:
		if strings.TrimSpace(raw) == "" {
			return "dev"
		}
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func firstLogValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
