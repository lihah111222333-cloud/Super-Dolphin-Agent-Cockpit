package promptadapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/internal/storeguard"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/uipreference"
)

var (
	errPromptPreferenceStoreNotConfigured = errors.New("prompt preference store is not configured")
	errPromptSharedFileStoreNotConfigured = errors.New("prompt shared file store is not configured")
)

type promptPreferenceReaderAdapter struct {
	store uipreference.Store
}

type promptSharedFileReaderAdapter struct {
	store sharedfilestore.Reader
}

var (
	_ prompt.PreferenceReader = (*promptPreferenceReaderAdapter)(nil)
	_ prompt.SharedFileReader = (*promptSharedFileReaderAdapter)(nil)
)

// providePromptPreferenceReader 把 UI preference Store 收窄为 prompt runtime 的只读端口。
func providePromptPreferenceReader(store uipreference.Store) (prompt.PreferenceReader, error) {
	if storeguard.IsNil(store) {
		return nil, errPromptPreferenceStoreNotConfigured
	}
	return &promptPreferenceReaderAdapter{store: store}, nil
}

// providePromptSharedFileReader 把 shared file Store 收窄为 prompt runtime 的内容读取端口。
func providePromptSharedFileReader(store sharedfilestore.Reader) (prompt.SharedFileReader, error) {
	if storeguard.IsNil(store) {
		return nil, errPromptSharedFileStoreNotConfigured
	}
	return &promptSharedFileReaderAdapter{store: store}, nil
}

// GetValue 读取偏好并复制 RawMessage，避免领域层与 Store 共享 backing array。
func (a *promptPreferenceReaderAdapter) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	value, err := a.store.GetValue(ctx, cwd, key)
	if err != nil {
		return nil, err
	}
	return clonePromptJSON(value), nil
}

// GetContent 只向领域暴露内容；成功的 nil 文件保持为空字符串语义。
func (a *promptSharedFileReaderAdapter) GetContent(ctx context.Context, path string) (string, error) {
	file, err := a.store.Get(ctx, path)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", nil
	}
	return file.Content, nil
}

func clonePromptJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
