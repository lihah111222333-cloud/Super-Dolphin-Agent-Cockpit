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

// ConfigureServiceFromEnv 从环境变量读取 service metadata，并更新 runtime。
func (r *Runtime) ConfigureServiceFromEnv(defaultVersion string) {
	r.SetServiceMetadata(
		firstLogValue(os.Getenv(serviceNameEnv), "super-dolphin"),
		firstLogValue(os.Getenv(serviceVersionEnv), os.Getenv(updateVersionEnv), defaultVersion, "dev"),
		normalizeLogEnv(firstLogValue(os.Getenv(serviceEnvEnv), os.Getenv(appEnvEnv), os.Getenv(runtimeModeEnv), "dev")),
	)
}

// SetServiceMetadata 更新 runtime service metadata，并重建当前日志器让后续日志带上新字段。
func (r *Runtime) SetServiceMetadata(name, version, env string) {
	name = firstLogValue(name, "super-dolphin")
	version = firstLogValue(version, "dev")
	env = normalizeLogEnv(firstLogValue(env, "dev"))

	r.mu.Lock()
	r.serviceName = name
	r.serviceVersion = version
	r.env = env
	r.mu.Unlock()

	r.rebuildActiveLogger()
}

func (r *Runtime) applyGlobalAttrs(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return nil
	}
	attrs := r.currentGlobalAttrs()
	if len(attrs) == 0 {
		return logger
	}
	return logger.With(attrs...)
}

func (r *Runtime) currentGlobalAttrs() []any {
	r.mu.Lock()
	project := strings.TrimSpace(r.project)
	serviceName := strings.TrimSpace(r.serviceName)
	serviceVersion := strings.TrimSpace(r.serviceVersion)
	env := strings.TrimSpace(r.env)
	r.mu.Unlock()

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

func (r *Runtime) rebuildActiveLogger() {
	r.mu.Lock()
	f := r.logFile
	mode := r.activeMode
	level := r.activeLevel
	r.mu.Unlock()
	if f != nil {
		r.rebuildLoggerWithFile(f)
		return
	}
	r.storeLogger(r.newLogger(mode, level))
}

// normalizeLogEnv 将常见环境别名收敛为 dev/test/prod，其余值保持小写透传。
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

// firstLogValue 返回第一个非空白值，供环境变量优先级选择使用。
func firstLogValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
