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

// ConfigureServiceFromEnv 从环境变量读取 service metadata，并用 defaultVersion 兜住版本缺失。
func ConfigureServiceFromEnv(defaultVersion string) {
	SetServiceMetadata(
		firstLogValue(os.Getenv(serviceNameEnv), "super-dolphin"),
		firstLogValue(os.Getenv(serviceVersionEnv), os.Getenv(updateVersionEnv), defaultVersion, "dev"),
		normalizeLogEnv(firstLogValue(os.Getenv(serviceEnvEnv), os.Getenv(appEnvEnv), os.Getenv(runtimeModeEnv), "dev")),
	)
}

// SetServiceMetadata 更新全局 service metadata，并重建当前日志器让后续日志带上新字段。
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

// applyGlobalAttrs 为新建日志器绑定当前 service/env/project 字段。
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

// currentGlobalAttrs 在锁内复制全局 metadata，避免 logger 重建时读到半更新状态。
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

// rebuildActiveLogger 使用当前模式、级别和文件状态重建全局日志器。
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
