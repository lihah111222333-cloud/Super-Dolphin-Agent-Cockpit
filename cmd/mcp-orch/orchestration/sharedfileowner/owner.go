package sharedfileowner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
)

var ErrInvalidOwner = errors.New("invalid sharedfile owner")

type Owner struct {
	DagKey   string
	NodeKey  string
	RunID    int64
	ThreadID string
	TurnID   string
}

type marker struct {
	DagKey    string `json:"dag_key"`
	NodeKey   string `json:"node_key"`
	RunID     int64  `json:"run_id"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	UpdatedAt string `json:"updated_at"`
}

func HasCurrent(ctx context.Context, reader nodeexec.SharedFileReader, path string, owner Owner) (bool, error) {
	if reader == nil {
		return false, errors.New("SharedFileReader not wired")
	}
	if err := validate(owner); err != nil {
		return false, err
	}
	if _, exists, err := reader.ReadSharedFile(ctx, path); err != nil || !exists {
		return false, err
	}
	raw, exists, err := reader.ReadSharedFile(ctx, MarkerPath(path))
	if err != nil || !exists {
		return false, err
	}
	return matches(raw, owner), nil
}

func Write(ctx context.Context, writer nodeexec.SharedFileWriter, path, content string, owner Owner) error {
	if writer == nil {
		return errors.New("SharedFileWriter not wired")
	}
	if err := validate(owner); err != nil {
		return err
	}
	if err := writer.WriteSharedFile(ctx, path, content); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	markerPath := MarkerPath(path)
	if err := writer.WriteSharedFile(ctx, markerPath, encode(owner)); err != nil {
		return fmt.Errorf("write owner marker %q: %w", markerPath, err)
	}
	return nil
}

func MarkerPath(path string) string {
	return "_internal/dag-output-ownership/" + strings.TrimSpace(path) + ".metadata.json"
}

func IsValidation(err error) bool { return errors.Is(err, ErrInvalidOwner) }

func validate(owner Owner) error {
	switch {
	case strings.TrimSpace(owner.DagKey) == "":
		return fmt.Errorf("%w: missing dag_key", ErrInvalidOwner)
	case strings.TrimSpace(owner.NodeKey) == "":
		return fmt.Errorf("%w: missing node_key", ErrInvalidOwner)
	case owner.RunID <= 0:
		return fmt.Errorf("%w: missing run_id", ErrInvalidOwner)
	case strings.TrimSpace(owner.ThreadID) == "":
		return fmt.Errorf("%w: missing thread_id", ErrInvalidOwner)
	case strings.TrimSpace(owner.TurnID) == "":
		return fmt.Errorf("%w: missing turn_id", ErrInvalidOwner)
	}
	return nil
}

func encode(owner Owner) string {
	raw, _ := json.Marshal(marker{
		DagKey:    strings.TrimSpace(owner.DagKey),
		NodeKey:   strings.TrimSpace(owner.NodeKey),
		RunID:     owner.RunID,
		ThreadID:  strings.TrimSpace(owner.ThreadID),
		TurnID:    strings.TrimSpace(owner.TurnID),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	return string(raw)
}

func matches(raw string, owner Owner) bool {
	var got marker
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &got); err != nil {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(got.UpdatedAt)); err != nil {
		return false
	}
	return got.DagKey == strings.TrimSpace(owner.DagKey) && got.NodeKey == strings.TrimSpace(owner.NodeKey) &&
		got.RunID == owner.RunID && got.ThreadID == strings.TrimSpace(owner.ThreadID) && got.TurnID == strings.TrimSpace(owner.TurnID)
}
