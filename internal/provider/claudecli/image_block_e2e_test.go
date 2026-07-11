//go:build e2e_vision

package claudecli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// Run with: go test -tags=e2e_vision ./internal/provider/claudecli/ -run TestVisionEndToEnd -v -count=1
//
// Requires: `claude` CLI on PATH and a logged-in session. This test makes a
// real API call (small token cost) and is skipped from the normal test suite.
func TestVisionEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	dir := t.TempDir()
	pngPath := filepath.Join(dir, "tiny.png")
	raw, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}
	if err := os.WriteFile(pngPath, raw, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: pngPath},
			{Type: "text", Content: "In one word, what color dominates this image? Reply with just the word."},
		},
	}, nil)
	if len(blocks) != 2 || blocks[0]["type"] != "image" {
		t.Fatalf("unexpected blocks: %+v", blocks)
	}

	payload, err := marshalTurnContentPayload(blocks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
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
		t.Fatalf("claude CLI run: %v\noutput: %s", err, out.String())
	}

	if !sawClaudeVisionSuccess(t, out.String()) {
		t.Fatalf("no result event in output: %s", out.String())
	}
}

func sawClaudeVisionSuccess(t *testing.T, output string) bool {
	t.Helper()

	var sawSuccess bool
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
		if evt.Type == "result" {
			if evt.IsError {
				t.Fatalf("claude returned error: %s", line)
			}
			if evt.Subtype != "success" {
				t.Fatalf("claude result subtype = %q, want success: %s", evt.Subtype, line)
			}
			t.Logf("claude reply: %q", evt.Result)
			sawSuccess = true
		}
	}
	return sawSuccess
}
