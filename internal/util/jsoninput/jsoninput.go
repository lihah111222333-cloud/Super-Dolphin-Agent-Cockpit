// Package jsoninput 提供工具输入共用的严格 JSON 解码入口。
package jsoninput

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeStrictObject 解码工具输入；空值和 null 按空对象处理，未知字段和尾随内容会报错。
func DecodeStrictObject(input json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode input: trailing JSON payload")
	}
	return nil
}
