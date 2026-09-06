// Package codexwire 提供官方 Codex CLI JSONL 与 jrpc2 的有界协议适配。
package codexwire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/creachadair/jrpc2"
)

const maxFrameBytes = 4 * 1024 * 1024

// Channel 只处理 Codex 省略 jsonrpc 字段的线协议，不拥有业务 session 或任务状态。
type Channel struct {
	reader    *bufio.Scanner
	input     io.WriteCloser
	output    io.ReadCloser
	closeOnce sync.Once
	closeErr  error
	onNotify  func(*jrpc2.Request)
}

// NewChannel 绑定子进程标准流，并在接收分配前设置 frame 硬上限。
func NewChannel(output io.ReadCloser, input io.WriteCloser) *Channel {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), maxFrameBytes)
	return &Channel{reader: scanner, input: input, output: output}
}

// Send 去掉 JSON-RPC 版本字段；不改写 method、id 或 payload。
func (c *Channel) Send(raw []byte) error {
	if len(raw) > maxFrameBytes {
		return errors.New("Codex request frame exceeds limit")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	delete(envelope, "jsonrpc")
	frame, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	frame = append(frame, '\n')
	written, err := c.input.Write(frame)
	if err == nil && written != len(frame) {
		return io.ErrShortWrite
	}
	return err
}

// Recv 校验完整 JSON frame 后补上 jrpc2 要求的版本，不容忍 stdout 日志污染。
func (c *Channel) Recv() ([]byte, error) {
	for {
		frame, err := c.recvFrame()
		if err != nil {
			return nil, err
		}
		consumed, err := c.deliverNotification(frame)
		if err != nil {
			return nil, err
		}
		if !consumed {
			return frame, nil
		}
	}
}

// recvFrame 读取并规范化一条有界 JSONL，不引入并发分发。
func (c *Channel) recvFrame() ([]byte, error) {
	if !c.reader.Scan() {
		if err := c.reader.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(c.reader.Bytes(), &envelope); err != nil || envelope == nil {
		return nil, errors.New("invalid Codex JSONL envelope")
	}
	envelope["jsonrpc"] = json.RawMessage(`"2.0"`)
	return json.Marshal(envelope)
}

// deliverNotification 在读下一帧前交付通知，防止 jrpc2 的异步响应分发改变 delta 顺序。
// 回调与原 OnNotify 契约相同，不能同步等待同一 client 的 RPC 或 Close。
// 请求响应仍由 jrpc2 管理；不把有 ID 的反向请求当作通知吞掉。
func (c *Channel) deliverNotification(frame []byte) (bool, error) {
	if c.onNotify == nil {
		return false, nil
	}
	var envelope struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return false, err
	}
	if envelope.Method == "" {
		return false, nil
	}
	normalized, err := json.Marshal(struct {
		Version string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{Version: "2.0", Method: envelope.Method, Params: envelope.Params})
	if err != nil {
		return false, fmt.Errorf("normalize Codex notification: %w", err)
	}
	requests, err := jrpc2.ParseRequests(normalized)
	if err != nil {
		return false, fmt.Errorf("parse Codex notification: %w", err)
	}
	if len(requests) != 1 {
		return false, errors.New("Codex notification frame must contain one request")
	}
	if requests[0].Error != nil {
		return false, fmt.Errorf("invalid Codex notification request: %w", requests[0].Error)
	}
	if requests[0].ID != "" {
		return false, errors.New("Codex notification unexpectedly has request id")
	}
	c.onNotify(requests[0].ToRequest())
	return true, nil
}

// Close 同时关闭两端管道，使阻塞接收与发送都可退出。
func (c *Channel) Close() error {
	c.closeOnce.Do(func() { c.closeErr = errors.Join(c.input.Close(), c.output.Close()) })
	return c.closeErr
}
