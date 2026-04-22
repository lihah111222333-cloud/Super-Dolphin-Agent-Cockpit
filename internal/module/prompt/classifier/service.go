package classifier

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvEnabled is the global on/off switch. When unset or falsy, NewService
	// returns NoopClassifier and callers should see classifier.Enabled()=false.
	EnvEnabled = "ENABLE_PROMPT_CLASSIFIER"
	// EnvModel overrides the claude model used. Default: "haiku".
	EnvModel = "PROMPT_CLASSIFIER_MODEL"
	// EnvTimeoutSeconds overrides the classifier timeout. Default: 30s.
	EnvTimeoutSeconds = "PROMPT_CLASSIFIER_TIMEOUT_SEC"
)

// Config is the classifier factory input. All fields have env fallbacks so
// the fx wire-up is just NewConfigFromEnv + NewService.
type Config struct {
	Enabled bool
	// Binary is the claude CLI path. Empty = "claude" on PATH.
	Binary string
	// Model is a claude alias or full name. Empty = "haiku".
	Model string
	// Timeout caps the subprocess wall-clock. Zero = 30s.
	Timeout time.Duration
}

// NewConfigFromEnv reads the classifier settings from env. The thread module
// constructs this at fx wire-up time and passes it to NewService.
func NewConfigFromEnv() Config {
	return Config{
		Enabled: parseEnvBool(EnvEnabled, false),
		Model:   strings.TrimSpace(os.Getenv(EnvModel)),
		Timeout: parseEnvDuration(EnvTimeoutSeconds, 0),
	}
}

// NewService returns the configured Classifier. It never returns nil; a
// disabled config maps to NoopClassifier so callers don't need a nil guard.
func NewService(cfg Config) Classifier {
	if !cfg.Enabled {
		return NoopClassifier{}
	}
	return NewClaudeCLIClassifier(cfg.Binary, cfg.Model, cfg.Timeout)
}

func parseEnvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseEnvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}
