package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const logFallbackDirEnv = "GO_AGENT_LOG_FALLBACK_DIR"

type localLogFallbackRecord struct {
	TS          int64          `json:"ts"`
	InstanceID  string         `json:"instance_id,omitempty"`
	Generation  uint64         `json:"generation,omitempty"`
	Seq         uint64         `json:"seq,omitempty"`
	BinaryName  string         `json:"binary_name,omitempty"`
	ClientKind  string         `json:"client_kind,omitempty"`
	Level       string         `json:"level"`
	Message     string         `json:"message"`
	Fields      map[string]any `json:"fields,omitempty"`
	SendError   string         `json:"send_error,omitempty"`
	FallbackFor string         `json:"fallback_for,omitempty"`
}

// InstallLogRelay bridges all pkg/logger records emitted by this peer process
// back to the control plane. The relay is deliberately installed at the logger
// layer rather than at individual call sites so diagnostic logs stay complete.
// InstallLogRelay 处理安装日志relay。
func (c *Client) InstallLogRelay() {
	if c == nil {
		return
	}
	pkglogger.SetRelayHook(func(ctx context.Context, payload pkglogger.RelayPayload) {
		_ = c.relayLog(ctx, payload)
	})
}

// relayLog 处理relay日志。
func (c *Client) relayLog(ctx context.Context, payload pkglogger.RelayPayload) error {
	message := strings.TrimSpace(payload.Msg)
	if message == "" {
		return nil
	}
	fields := cloneAnyMap(payload.Fields)
	if fields == nil {
		fields = map[string]any{}
	}
	if source := strings.TrimSpace(payload.SourceProcess); source != "" {
		fields["source_process"] = source
	} else if c.cfg.BinaryName != "" {
		fields["source_process"] = c.cfg.BinaryName
	}
	if c.cfg.BinaryName != "" {
		fields["binary_name"] = c.cfg.BinaryName
	}
	if c.cfg.ClientKind != "" {
		fields["client_kind"] = c.cfg.ClientKind
	}
	if c.cfg.AgentID != "" {
		fields["agent_id"] = c.cfg.AgentID
	}
	if c.cfg.ThreadID != "" {
		fields["thread_id"] = c.cfg.ThreadID
	}
	return c.LogFields(pkglogger.WithRelayDisabled(ctx), payload.Level, message, fields)
}

// Log 处理日志。
func (c *Client) Log(ctx context.Context, level, message string, fields map[string]string) error {
	return c.LogFields(ctx, level, message, cloneStringMapAny(fields))
}

// LogFields 处理日志字段。
func (c *Client) LogFields(ctx context.Context, level, message string, fields map[string]any) error {
	lease := c.currentLease()
	entry := mcp.LogNotify{
		InstanceID: lease.InstanceID,
		Generation: lease.Generation,
		Seq:        c.nextLogSeq(),
		Level:      normalizeLogLevel(level),
		Message:    strings.TrimSpace(message),
		Fields:     cloneAnyMap(fields),
		TS:         time.Now().UnixMilli(),
	}
	if entry.Message == "" {
		return nil
	}
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		c.localLogFallback(entry, nil)
		return nil
	}
	noteCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()
	if err := conn.Notify(noteCtx, mcp.MethodLog, entry); err != nil {
		c.localLogFallback(entry, err)
		if isTransportErr(err) {
			return fmt.Errorf("log notify failed after local fallback: %w", err)
		}
		return err
	}
	return nil
}

func normalizeLogLevel(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return "DEBUG"
	case "WARN", "WARNING":
		return "WARN"
	case "ERROR":
		return "ERROR"
	default:
		return "INFO"
	}
}

func (c *Client) localLogFallback(entry mcp.LogNotify, sendErr error) {
	if err := c.writeLocalLogFallback(entry, sendErr); err != nil {
		pkglogger.Get().Log(pkglogger.WithRelayDisabled(context.Background()), pkglogger.LevelWarn,
			"bootstrap local log fallback write failed",
			"instance_id", c.instanceID,
			"callback_method", mcp.MethodLog,
			"level", entry.Level,
			"message", entry.Message,
			"error", err,
		)
	}
}

// writeLocalLogFallback 写入local日志兜底。
func (c *Client) writeLocalLogFallback(entry mcp.LogNotify, sendErr error) error {
	dir := c.localLogFallbackDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, c.localLogFallbackFileName())
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	ts := entry.TS
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	record := localLogFallbackRecord{
		TS:          ts,
		InstanceID:  shared.FirstNonEmpty(entry.InstanceID, c.instanceID),
		Generation:  entry.Generation,
		Seq:         entry.Seq,
		BinaryName:  c.cfg.BinaryName,
		ClientKind:  c.cfg.ClientKind,
		Level:       normalizeLogLevel(entry.Level),
		Message:     strings.TrimSpace(entry.Message),
		Fields:      cloneAnyMap(entry.Fields),
		FallbackFor: mcp.MethodLog,
	}
	if sendErr != nil {
		record.SendError = sendErr.Error()
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func (c *Client) localLogFallbackDir() string {
	if override := strings.TrimSpace(os.Getenv(logFallbackDirEnv)); override != "" {
		return override
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	logDir, _ := pkglogger.ResolveProjectLogDir(home, cwd)
	return filepath.Join(logDir, "peer-fallback")
}

func (c *Client) localLogFallbackFileName() string {
	binary := strings.TrimSpace(c.cfg.BinaryName)
	if binary == "" {
		binary = "mcp-peer"
	}
	binary = filepath.Base(binary)
	return binary + "-" + time.Now().Format("2006-01-02") + ".log"
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
