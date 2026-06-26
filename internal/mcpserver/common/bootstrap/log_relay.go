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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// logFallbackDirEnv 指定控制平面不可达时本地 fallback 日志的目录。
const logFallbackDirEnv = "GO_AGENT_LOG_FALLBACK_DIR"

// localLogFallbackRecord 是写入本地 fallback 日志文件的行结构。
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

// InstallLogRelay 在 logger 层安装 relay hook，把当前 peer 进程日志转发到控制平面。
// relay 放在 logger 层而不是调用点上，保证诊断日志覆盖完整。
func (c *Client) InstallLogRelay() {
	if c == nil {
		return
	}
	pkglogger.SetRelayHook(func(ctx context.Context, payload pkglogger.RelayPayload) {
		_ = c.relayLog(ctx, payload)
	})
}

// relayLog 丰富日志字段后发送到控制平面，递归 relay 会通过 WithRelayDisabled 阻断。
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

// Log 发送 string map 形式日志字段，内部转换为 LogFields 的通用 any map。
func (c *Client) Log(ctx context.Context, level, message string, fields map[string]string) error {
	return c.LogFields(ctx, level, message, cloneStringMapAny(fields))
}

// LogFields 发送结构化日志；控制平面不可达时写本地 fallback 文件。
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

// normalizeLogLevel 将日志级别字符串规范化为大写标准值。
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

// localLogFallback 在控制平面不可达时将日志写入本地文件，失败时记录警告日志。
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

// writeLocalLogFallback 以 JSONL 追加本地 fallback 日志，保留发送失败原因用于排障。
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

// localLogFallbackDir 解析本地日志 fallback 目录路径。
func (c *Client) localLogFallbackDir() string {
	if override := strings.TrimSpace(os.Getenv(logFallbackDirEnv)); override != "" {
		return override
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	logDir, _ := pkglogger.ResolveProjectLogDir(home, cwd)
	return filepath.Join(logDir, "peer-fallback")
}

// localLogFallbackFileName 返回本地 fallback 日志的文件名（按 binary 和日期命名）。
func (c *Client) localLogFallbackFileName() string {
	binary := strings.TrimSpace(c.cfg.BinaryName)
	if binary == "" {
		binary = "mcp-peer"
	}
	binary = filepath.Base(binary)
	return binary + "-" + time.Now().Format("2006-01-02") + ".log"
}

// cloneAnyMap 深拷贝 map[string]any，nil/空输入返回 nil，空键跳过。
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
