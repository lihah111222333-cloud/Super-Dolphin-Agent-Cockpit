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

// MaxStdioMessageBytes 限制 stdio MCP 单条 JSON-RPC 消息大小。
const MaxStdioMessageBytes = 1 << 20

// errStdioMessageTooLarge 标记 stdio 单条消息超过固定上限。
var errStdioMessageTooLarge = errors.New("mcp stdio: message exceeds stdio message limit")

// stdio transport 模式常量，modeUnknown 表示尚未从输入流探测到 framing。
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
	rawCap  *stdioRawLimitReader
	writeMu sync.Mutex
}

// stdioRawLimitReader 为 raw JSON decoder 提供逐条消息的读取上限。
type stdioRawLimitReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

// Reset 重置单条 raw JSON 消息的读取预算。
func (r *stdioRawLimitReader) Reset() {
	r.remaining = MaxStdioMessageBytes + 1
	r.exceeded = false
}

// Read 从底层 reader 读取数据，并在预算耗尽后返回超限错误。
func (r *stdioRawLimitReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		r.exceeded = true
		return 0, stdioMessageTooLargeError(MaxStdioMessageBytes + 1)
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	if r.remaining <= 0 {
		r.exceeded = true
	}
	return n, err
}

// Exceeded 返回当前消息是否触达过读取上限。
func (r *stdioRawLimitReader) Exceeded() bool {
	return r != nil && r.exceeded
}

// NewStdioTransport 创建支持 raw JSON 与 Content-Length framed 两种模式的 stdio transport。
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

// ReadMessage 读取一条 MCP JSON-RPC 消息，首次读取时自动探测 framing 模式。
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

// WriteMessage 按已探测到的模式写出 JSON-RPC 消息，并串行化并发写入。
func (t *StdioTransport) WriteMessage(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		pkglogger.Warn("mcp stdio: marshal failed", "error", err)
		return err
	}
	if len(raw) > MaxStdioMessageBytes {
		return stdioMessageTooLargeError(len(raw))
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
			t.rawCap = &stdioRawLimitReader{reader: t.reader}
			t.rawCap.Reset()
			t.decoder = json.NewDecoder(t.rawCap)
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
	if t.rawCap != nil {
		t.rawCap.Reset()
	}
	if err := t.decoder.Decode(&raw); err != nil {
		if errors.Is(err, errStdioMessageTooLarge) {
			return nil, stdioMessageTooLargeError(MaxStdioMessageBytes + 1)
		}
		return nil, err
	}
	if len(raw) > MaxStdioMessageBytes || t.rawCap.Exceeded() {
		return nil, stdioMessageTooLargeError(len(raw))
	}
	return append(json.RawMessage(nil), raw...), nil
}

// readFramed 读取 Content-Length framed 消息，并拒绝缺失或非法长度头。
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
		parsed, err := parseStdioContentLengthHeader(line, length)
		if err != nil {
			return nil, err
		}
		length = parsed
	}
	if length < 0 {
		return nil, errors.New("mcp stdio: missing Content-Length header")
	}
	if length > MaxStdioMessageBytes {
		return nil, stdioMessageTooLargeError(length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(t.reader, body); err != nil {
		return nil, err
	}
	return validateFramedJSON(body)
}

// parseStdioContentLengthHeader 解析单行 framed header，并忽略非 Content-Length 头。
func parseStdioContentLengthHeader(line string, current int) (int, error) {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return current, fmt.Errorf("mcp stdio: malformed header %q", line)
	}
	if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
		return current, nil
	}
	length, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || length < 0 {
		return current, fmt.Errorf("mcp stdio: invalid Content-Length %q", value)
	}
	return length, nil
}

// validateFramedJSON 校验 framed 模式下读取的 body 是否为合法 JSON。
func validateFramedJSON(body []byte) (json.RawMessage, error) {
	if !json.Valid(body) {
		return nil, errors.New("mcp stdio: malformed framed JSON")
	}
	return json.RawMessage(body), nil
}

// stdioMessageTooLargeError 返回带有实际长度和上限的 stdio 超限错误。
func stdioMessageTooLargeError(size int) error {
	return fmt.Errorf("%w: size %d limit %d", errStdioMessageTooLarge, size, MaxStdioMessageBytes)
}

// flushWriter 在 writer 支持 Flush 时刷新缓冲；普通 writer 不需要额外收尾。
func flushWriter(writer io.Writer) error {
	flusher, ok := writer.(interface{ Flush() error })
	if !ok {
		return nil
	}
	return flusher.Flush()
}
