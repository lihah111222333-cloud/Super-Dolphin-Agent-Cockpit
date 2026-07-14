package shared

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// DecodeHistoryFields 严格解码持久化历史的时间戳和元数据，拒绝格式损坏的记录。
func DecodeHistoryFields(timestamp string, raw json.RawMessage) (time.Time, map[string]any, error) {
	var parsed time.Time
	if value := strings.TrimSpace(timestamp); value != "" {
		var err error
		parsed, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, nil, errors.New("invalid history timestamp")
		}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return parsed, nil, nil
	}
	var metadata map[string]any
	if err := json.Unmarshal(trimmed, &metadata); err != nil || metadata == nil {
		return time.Time{}, nil, errors.New("history metadata must be a JSON object")
	}
	return parsed, metadata, nil
}
