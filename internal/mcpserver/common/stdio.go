package common

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// transportMode 表示 stdio 传输的帧格式模式。
type transportMode int

const (
	modeUnknown transportMode = iota
	modeRawJSON
	modeFramed
)

// StdioTransport 实现基于 stdin/stdout 的 MCP 消息传输，自动检测 framed 或 raw JSON 模式。
type StdioTransport struct {
	reader  *bufio.Reader
	writer  io.Writer
	closer  io.Closer
	decoder *json.Decoder
	mode    transportMode
	writeMu sync.Mutex
}

// NewStdioTransport 创建stdio传输。
func NewStdioTransport(stdin io.Reader, stdout io.Writer) *StdioTransport {
	transport := &StdioTransport{
		reader: bufio.NewReader(stdin),
		writer: stdout,
	}
	if closer, ok := stdin.(io.Closer); ok {
		transport.closer = closer
	}
	return transport
}

// Close 关闭MCP 服务资源。
func (t *StdioTransport) Close() error {
	if t == nil || t.closer == nil {
		return nil
	}
	return t.closer.Close()
}

// ReadMessage 读取消息。
func (t *StdioTransport) ReadMessage() (json.RawMessage, error) {
	if err := t.ensureMode(); err != nil {
		pkglogger.Warn("mcp stdio: read mode detection failed", "error", err)
		return nil, err
	}
	var msg json.RawMessage
	var err error
	if t.mode == modeFramed {
		msg, err = t.readFramed()
	} else {
		msg, err = t.readRaw()
	}
	if err != nil {
		pkglogger.Warn("mcp stdio: read failed",
			"mode", t.mode, "error", err)
	}
	return msg, err
}

// WriteMessage 写入消息。
func (t *StdioTransport) WriteMessage(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		pkglogger.Warn("mcp stdio: marshal failed", "error", err)
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.mode == modeFramed {
		if _, err := fmt.Fprintf(t.writer, "Content-Length: %d\r\n\r\n", len(raw)); err != nil {
			pkglogger.Warn("mcp stdio: write header failed", "error", err)
			return err
		}
		if _, err := t.writer.Write(raw); err != nil {
			pkglogger.Warn("mcp stdio: write body failed", "error", err)
			return err
		}
		return flushWriter(t.writer)
	}
	if _, err := t.writer.Write(append(raw, '\n')); err != nil {
		pkglogger.Warn("mcp stdio: write raw failed", "error", err)
		return err
	}
	return flushWriter(t.writer)
}

// ensureMode 确保模式。
func (t *StdioTransport) ensureMode() error {
	if t.mode != modeUnknown {
		return nil
	}
	for {
		peeked, err := t.reader.Peek(1)
		if err != nil {
			return err
		}
		switch peeked[0] {
		case ' ', '\n', '\r', '\t':
			if _, err := t.reader.ReadByte(); err != nil {
				return err
			}
		case '{', '[':
			t.mode = modeRawJSON
			t.decoder = json.NewDecoder(t.reader)
			return nil
		default:
			t.mode = modeFramed
			return nil
		}
	}
}

// readRaw 使用 json.Decoder 读取一条 JSON 对象，适用于 raw JSON 模式。
func (t *StdioTransport) readRaw() (json.RawMessage, error) {
	var raw json.RawMessage
	if err := t.decoder.Decode(&raw); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), raw...), nil
}

// readFramed 读取framed。
func (t *StdioTransport) readFramed() (json.RawMessage, error) {
	length := -1
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("mcp stdio: malformed header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("mcp stdio: invalid Content-Length %q", value)
		}
	}
	if length < 0 {
		return nil, errors.New("mcp stdio: missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(t.reader, body); err != nil {
		return nil, err
	}
	return validateFramedJSON(body)
}

// validateFramedJSON 校验 framed 模式下读取的 body 是否为合法 JSON。
func validateFramedJSON(body []byte) (json.RawMessage, error) {
	if !json.Valid(body) {
		return nil, errors.New("mcp stdio: malformed framed JSON")
	}
	return json.RawMessage(body), nil
}

// flushWriter 若 writer 实现了 Flush() error 则执行 flush，否则直接返回。
func flushWriter(writer io.Writer) error {
	flusher, ok := writer.(interface{ Flush() error })
	if !ok {
		return nil
	}
	return flusher.Flush()
}
