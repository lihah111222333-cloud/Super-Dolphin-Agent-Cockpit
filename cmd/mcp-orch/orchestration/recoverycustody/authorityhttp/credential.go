package authorityhttp

import (
	"errors"
	"fmt"
	"io"
)

const redactedCredential = "[REDACTED]"

// BearerCredential 私有保存 opaque bearer token，并阻止常见格式化与 JSON 路径泄漏原值。
type BearerCredential struct {
	value string
}

// NewBearerCredential 校验并封装显式 bearer token，不在错误中回显输入。
func NewBearerCredential(value string) (BearerCredential, error) {
	if !validBearerToken(value) {
		return BearerCredential{}, errors.New("recovery custody authority HTTP bearer credential is invalid")
	}
	return BearerCredential{value: value}, nil
}

// String 为普通格式化返回固定脱敏文本。
func (BearerCredential) String() string { return redactedCredential }

// GoString 为 Go-syntax 格式化返回固定脱敏文本。
func (BearerCredential) GoString() string { return redactedCredential }

// Format 覆盖全部 fmt verb，避免嵌套 Config 打印递归读取私有字段。
func (BearerCredential) Format(state fmt.State, _ rune) {
	if _, err := io.WriteString(state, redactedCredential); err != nil {
		return
	}
}

// MarshalJSON 只编码固定脱敏文本，不序列化私有 token。
func (BearerCredential) MarshalJSON() ([]byte, error) {
	return []byte(`"[REDACTED]"`), nil
}
