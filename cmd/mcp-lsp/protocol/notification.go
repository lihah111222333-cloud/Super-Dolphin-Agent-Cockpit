package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrUnsupportedNotification = errors.New("protocol: unsupported notification")
	ErrNotificationHandlerNil  = errors.New("protocol: notification handler is nil")
)

// LogMessageType 是 window/logMessage 的 LSP 日志级别枚举。
type LogMessageType int

// LogMessageType 常量与 LSP 规范中 window/logMessage 的级别值保持一致。
const (
	LogMessageError LogMessageType = iota + 1
	LogMessageWarning
	LogMessageInfo
	LogMessageLog
)

// LogMessageParams 是 window/logMessage 通知参数。
type LogMessageParams struct {
	Type    LogMessageType `json:"type"`
	Message string         `json:"message"`
}

// ShowMessageParams is the non-fatal window/showMessage notification shape.
// Message follows the same compatibility rule as LogMessageParams.
type ShowMessageParams struct {
	Type    LogMessageType `json:"type"`
	Message string         `json:"message"`
}

func (p *LogMessageParams) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    LogMessageType  `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var m messageValue
	if err := m.UnmarshalJSON(raw.Message); err != nil {
		return err
	}
	p.Type, p.Message = raw.Type, string(m)
	return nil
}

func (p *ShowMessageParams) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    LogMessageType  `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var m messageValue
	if err := m.UnmarshalJSON(raw.Message); err != nil {
		return err
	}
	p.Type, p.Message = raw.Type, string(m)
	return nil
}

type messageValue string

func (m *messageValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		*m = ""
		return nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
		*m = messageValue(value)
		return nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return err
	}
	*m = messageValue(compact.String())
	return nil
}

// NotificationHandler 接收语言服务器主动推送的诊断和日志通知。
type NotificationHandler interface {
	PublishDiagnostics(PublishDiagnosticsParams) error
	LogMessage(LogMessageParams) error
}

// String 返回日志级别的稳定英文名称。
func (t LogMessageType) String() string {
	switch t {
	case LogMessageError:
		return "error"
	case LogMessageWarning:
		return "warning"
	case LogMessageInfo:
		return "info"
	case LogMessageLog:
		return "log"
	default:
		return "unknown"
	}
}

// DispatchNotification 解码通知并分派到 handler，未知方法会返回可识别错误。
func DispatchNotification(payload []byte, handler NotificationHandler) error {
	if handler == nil {
		return ErrNotificationHandlerNil
	}
	notification, err := DecodeNotification(payload)
	if err != nil {
		return err
	}
	switch notification.Method {
	case MethodPublishDiagnostics:
		params, err := decodeNotificationParams[PublishDiagnosticsParams](notification.Params)
		if err != nil {
			return err
		}
		return handler.PublishDiagnostics(params)
	case MethodLogMessage:
		params, err := decodeNotificationParams[LogMessageParams](notification.Params)
		if err != nil {
			return err
		}
		return handler.LogMessage(params)
	case MethodShowMessage:
		// showMessage is informational; validate its payload so malformed JSON
		// remains visible, but never make an unsupported UI surface fatal.
		if _, err := decodeNotificationParams[ShowMessageParams](notification.Params); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s", ErrUnsupportedNotification, notification.Method)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedNotification, notification.Method)
	}
}

// decodeNotificationParams 解码通知参数，空参数按该类型零值处理。
func decodeNotificationParams[T any](raw json.RawMessage) (T, error) {
	var params T
	if len(raw) == 0 {
		return params, nil
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, fmt.Errorf("decode notification params: %w", err)
	}
	return params, nil
}
