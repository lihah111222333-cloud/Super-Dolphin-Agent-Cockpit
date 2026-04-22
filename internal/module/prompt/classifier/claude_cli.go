package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// claudeCLIClassifier runs `claude -p --model <model>` as a subprocess, feeds
// the classifier prompt on stdin, parses the JSON line from stdout.
//
// Auth: inherits whatever mechanism the user's claude CLI is configured with
// (OAuth session, keychain, ANTHROPIC_API_KEY, etc.). We deliberately do NOT
// pass --bare so the OAuth path stays available; users without an API key
// will have their session reused.
type claudeCLIClassifier struct {
	binary  string
	model   string
	timeout time.Duration
}

// NewClaudeCLIClassifier constructs a subprocess-backed classifier. Returns
// NoopClassifier when the claude binary is not on PATH so callers can always
// depend on the returned Classifier being non-nil and safe to call.
func NewClaudeCLIClassifier(binary, model string, timeout time.Duration) Classifier {
	bin := strings.TrimSpace(binary)
	if bin == "" {
		bin = "claude"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return NoopClassifier{}
	}
	m := strings.TrimSpace(model)
	if m == "" {
		m = "haiku"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &claudeCLIClassifier{binary: bin, model: m, timeout: timeout}
}

func (c *claudeCLIClassifier) Enabled() bool { return true }

func (c *claudeCLIClassifier) Classify(ctx context.Context, in Input) (Result, error) {
	if strings.TrimSpace(in.UserInput) == "" {
		return Result{}, errors.New("classifier: user_input required")
	}
	if len(in.Candidates) == 0 {
		return Result{}, errors.New("classifier: candidates required")
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	prompt := buildClassifierPrompt(in)
	cmd := exec.CommandContext(ctx, c.binary, "-p", "--model", c.model, "--output-format", "text")
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("classifier: claude exec: %w (stderr=%s)", err, trimForLog(stderr.String()))
	}

	picked, reason, perr := parseClassifierOutput(stdout.String())
	if perr != nil {
		return Result{}, fmt.Errorf("classifier: parse: %w (raw=%s)", perr, trimForLog(stdout.String()))
	}
	return Result{
		PromptKey: picked,
		Reason:    reason,
		Latency:   time.Since(start),
		Model:     c.model,
	}, nil
}

// parseClassifierOutput is tolerant to the two shapes claude -p commonly
// emits for "Reply with ONLY a JSON object":
//
//  1. Bare JSON: {"prompt_key":"...","reason":"..."}
//  2. Fenced:   ```json\n{"prompt_key":"...","reason":"..."}\n```
//
// Any other noise is rejected rather than silently guessed.
func parseClassifierOutput(raw string) (promptKey, reason string, err error) {
	body := strings.TrimSpace(raw)
	body = stripCodeFence(body)
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", errors.New("empty response")
	}
	var decoded struct {
		PromptKey string `json:"prompt_key"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return "", "", fmt.Errorf("invalid json: %w", err)
	}
	return strings.TrimSpace(decoded.PromptKey), strings.TrimSpace(decoded.Reason), nil
}

// stripCodeFence removes a leading ```<lang> line and trailing ``` line when
// claude wraps the JSON in markdown despite the instruction.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the first line (```json or similar).
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[idx+1:]
	}
	// Drop trailing fence.
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return s
}

func trimForLog(s string) string {
	s = strings.TrimSpace(s)
	const max = 200
	if len(s) > max {
		return s[:max] + "...(truncated)"
	}
	return s
}
