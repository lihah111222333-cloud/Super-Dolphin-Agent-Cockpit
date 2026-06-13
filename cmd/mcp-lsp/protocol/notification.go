package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrUnsupportedNotification = errors.New("protocol: unsupported notification")
	ErrNotificationHandlerNil  = errors.New("protocol: notification handler is nil")
)

type LogMessageType int

const (
	LogMessageError LogMessageType = iota + 1
	LogMessageWarning
	LogMessageInfo
	LogMessageLog
)

type LogMessageParams struct {
	Type    LogMessageType `json:"type"`
	Message string         `json:"message"`
}

type NotificationHandler interface {
	PublishDiagnostics(PublishDiagnosticsParams) error
	LogMessage(LogMessageParams) error
}

// String 返回字符串表示。
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

// DispatchNotification 派发notification。
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
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedNotification, notification.Method)
	}
}

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
