package classifier

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvDisabled force-disables the classifier regardless of whether the
	// claude CLI is on PATH. Used for tests, offline sandboxes, or when the
	// operator wants to be doubly sure the subprocess never runs. Default
	// semantics: when unset the backend auto-detects `claude` on PATH and
	// enables itself; the per-request UseClassifier flag remains the real
	// opt-in gate so no classification happens unless the UI asked for it.
	EnvDisabled = "DISABLE_PROMPT_CLASSIFIER"
	// EnvModel overrides the claude model used. Default: "haiku".
	EnvModel = "PROMPT_CLASSIFIER_MODEL"
	// EnvTimeoutSeconds overrides the classifier timeout. Default: 30s.
	EnvTimeoutSeconds = "PROMPT_CLASSIFIER_TIMEOUT_SEC"
	// EnvMaxCandidates caps the candidate list sent to the classifier.
	// Callers first prune with PruneCandidates() which scores by tag
	// overlap; the classifier then sees at most this many rows. Default 5.
	EnvMaxCandidates = "PROMPT_CLASSIFIER_MAX_CANDIDATES"
	// DefaultMaxCandidates is the fallback used when EnvMaxCandidates is unset
	// or invalid. 5 is a compromise between giving the LLM enough context to
	// distinguish overlapping personas and keeping haiku's response fast.
	DefaultMaxCandidates = 5
)

// MaxCandidatesFromEnv returns the configured candidate cap. The router uses
// this as the max argument to PruneCandidates before handing the list to
// the classifier.
func MaxCandidatesFromEnv() int {
	if n := parseEnvPositiveInt(EnvMaxCandidates); n > 0 {
		return n
	}
	return DefaultMaxCandidates
}

// Config is the classifier factory input. All fields have env fallbacks so
// the fx wire-up is just NewConfigFromEnv + NewService.
type Config struct {
	// Disabled, when true, forces NoopClassifier regardless of PATH. Default
	// is false — the factory auto-detects the claude binary instead.
	Disabled bool
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
		Disabled: parseEnvBool(EnvDisabled, false),
		Model:    strings.TrimSpace(os.Getenv(EnvModel)),
		Timeout:  parseEnvDuration(EnvTimeoutSeconds, 0),
	}
}

// NewService returns the configured Classifier. It never returns nil:
//   - DISABLE_PROMPT_CLASSIFIER=true forces NoopClassifier.
//   - Otherwise it tries to wire the claude CLI; missing binary
//     auto-degrades to NoopClassifier.
//
// This means the factory is safe to enable globally — the router still
// only calls the classifier when the per-request UseClassifier flag is set
// (that's the user-facing opt-in via the SystemPromptPage toggle), and
// missing `claude` never causes hard failures.
func NewService(cfg Config) Classifier {
	if cfg.Disabled {
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

func parseEnvPositiveInt(key string) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
