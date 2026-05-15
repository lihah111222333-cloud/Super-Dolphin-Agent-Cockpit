//go:build e2e_claude

package claudecli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Run with:
//
//	go test -tags=e2e_claude ./internal/provider/claudecli/ -run TestTurnResultLong_NotTruncated -v -count=1
//
// Requires:
//   - `claude` CLI on PATH
//   - logged-in claude session (or ANTHROPIC_API_KEY exported, depending on the
//     CLI version used). The CLI itself decides which auth path applies; this
//     test only requires that `claude -p` runs successfully against the real
//     backend.
//
// Purpose: produce empirical evidence about whether the claude CLI truncates
// the `result` field on the `type=result` stream-json event for long replies.
// The provider layer (internal/provider/claudecli/event_map.go) reads
// `dataString(raw.Data, "result")` and does not truncate — so any truncation
// observed here would originate inside the CLI binary itself.
//
// These tests intentionally only assert against the LOWER BOUND of the
// returned length (an exact-length assertion is impossible: the model may
// emit slightly more or fewer copies than asked). If the CLI is truncating,
// the lower-bound assertion will fail and the diagnostic output will print
// the head/tail of `result` so a human can decide whether the truncation is
// mid-content or simply a short reply from the model.

// stringRepeat builds the deterministic payload an agent is asked to echo.
// Using a 3-char chunk ("ABC") keeps the count math tidy: the resulting reply
// is approximately copies*3 bytes (the agent often adds a tiny prefix/suffix).
const longResultChunk = "ABC"

// expectedResultLowerBound returns the minimum acceptable Result length for
// `copies` repetitions of longResultChunk. We allow some slack because the
// model frequently wraps the response with framing text or omits a few copies
// — we only want to catch CLI-level truncation, not minor model variance.
func expectedResultLowerBound(copies int) int {
	// require at least 90% of the requested raw bytes.
	return (copies * len(longResultChunk) * 9) / 10
}

func runClaudeLongResultProbe(t *testing.T, copies int) {
	t.Helper()

	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	payload := buildClaudeLongPromptPayload(t, copies)

	// Long replies can take noticeably longer than vision tests — bump timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	out := runClaudeLongPromptCommand(t, ctx, copies, payload)
	resultText := parseClaudeLongResult(t, copies, out)
	assertClaudeLongResultNotTruncated(t, copies, resultText)
}

func buildClaudeLongPromptPayload(t *testing.T, copies int) []byte {
	t.Helper()
	// Build a stable prompt that asks for an exact, easy-to-verify pattern.
	prompt := fmt.Sprintf(
		"Please reply with EXACTLY %d concatenated copies of the 3-character string \"%s\" with no spaces, no newlines, no markdown, and no other text before or after. Just output the raw concatenated string. The total length should be exactly %d characters.",
		copies, longResultChunk, copies*len(longResultChunk),
	)
	payload, err := json.Marshal(newClaudeLongPromptWrapper(prompt))
	if err != nil {
		t.Fatalf("marshal prompt: %v", err)
	}
	return payload
}

func newClaudeLongPromptWrapper(prompt string) any {
	// Encode the prompt as a single user stream-json line — matches the shape
	// used by image_block_e2e_test.go so we exercise the same CLI mode the
	// provider drives in production.
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
	}
	type wrapper struct {
		Type    string  `json:"type"`
		Message message `json:"message"`
	}
	return wrapper{
		Type:    "user",
		Message: message{Role: "user", Content: []contentBlock{{Type: "text", Text: prompt}}},
	}
}

func runClaudeLongPromptCommand(t *testing.T, ctx context.Context, copies int, payload []byte) string {
	t.Helper()
	cmd := exec.CommandContext(ctx,
		"claude", "-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("claude CLI run (copies=%d): %v\noutput tail: %s", copies, err, tailString(out.String(), 600))
	}
	return out.String()
}

func parseClaudeLongResult(t *testing.T, copies int, output string) string {
	t.Helper()
	var sawSuccess bool
	var resultText string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var evt struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Type != "result" {
			continue
		}
		if evt.IsError {
			t.Fatalf("claude returned error event (copies=%d): %s", copies, line)
		}
		if evt.Subtype != "success" {
			t.Fatalf("claude result subtype = %q, want success (copies=%d): %s", evt.Subtype, copies, line)
		}
		resultText = evt.Result
		sawSuccess = true
		// keep iterating so multiple result events would each be inspected
	}
	if !sawSuccess {
		t.Fatalf("no result event in output (copies=%d). raw tail: %s", copies, tailString(output, 600))
	}
	return resultText
}

func assertClaudeLongResultNotTruncated(t *testing.T, copies int, resultText string) {
	t.Helper()
	lower := expectedResultLowerBound(copies)
	gotLen := len(resultText)
	sum := sha256.Sum256([]byte(resultText))
	hash := hex.EncodeToString(sum[:])

	t.Logf("copies=%d wantLowerBound=%d gotLen=%d sha256=%s",
		copies, lower, gotLen, hash)
	t.Logf("result head[:200]=%q", headString(resultText, 200))
	t.Logf("result tail[-200:]=%q", tailString(resultText, 200))

	if gotLen < lower {
		t.Fatalf(
			"Result truncated or unexpectedly short: copies=%d wantLowerBound=%d gotLen=%d\n"+
				"  sha256=%s\n"+
				"  head=%q\n"+
				"  tail=%q\n"+
				"This likely indicates CLI-level truncation; provider layer does not truncate.",
			copies, lower, gotLen, hash,
			headString(resultText, 200),
			tailString(resultText, 200),
		)
	}
}

func headString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestTurnResultLong_NotTruncated_3KB asks the model for ~3000 bytes of payload.
func TestTurnResultLong_NotTruncated_3KB(t *testing.T) {
	runClaudeLongResultProbe(t, 1000) // 1000 * "ABC" = 3000 bytes
}

// TestTurnResultLong_NotTruncated_8KB asks the model for ~8001 bytes of payload.
func TestTurnResultLong_NotTruncated_8KB(t *testing.T) {
	runClaudeLongResultProbe(t, 2667) // 2667 * "ABC" = 8001 bytes
}

// TestTurnResultLong_NotTruncated_16KB asks the model for ~16002 bytes of payload.
func TestTurnResultLong_NotTruncated_16KB(t *testing.T) {
	runClaudeLongResultProbe(t, 5334) // 5334 * "ABC" = 16002 bytes
}
