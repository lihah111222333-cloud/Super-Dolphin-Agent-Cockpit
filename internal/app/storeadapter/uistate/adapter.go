package uistateadapter

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/app/internal/storeguard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

type uiStatePreferenceStoreAdapter struct{ store uipreference.Store }
type uiStateSharedFileReaderAdapter struct{ reader sharedfilestore.Reader }
type uiStateBindingLookupAdapter struct{ store bindingstore.Store }

var (
	_ uistate.PreferenceStore  = (*uiStatePreferenceStoreAdapter)(nil)
	_ uistate.SharedFileReader = (*uiStateSharedFileReaderAdapter)(nil)
	_ uistate.BindingLookup    = (*uiStateBindingLookupAdapter)(nil)
)

// provideUIStatePreferenceStore 把 UI preference Store 收窄为 uistate 领域端口。
func provideUIStatePreferenceStore(store uipreference.Store) uistate.PreferenceStore {
	if storeguard.IsNil(store) {
		return nil
	}
	return &uiStatePreferenceStoreAdapter{store: store}
}

// provideUIStateSharedFileReader 把 shared file Store 收窄为 uistate 领域端口。
func provideUIStateSharedFileReader(reader sharedfilestore.Reader) uistate.SharedFileReader {
	if storeguard.IsNil(reader) {
		return nil
	}
	return &uiStateSharedFileReaderAdapter{reader: reader}
}

// provideUIStateBindingLookup 把 binding Store 收窄为 uistate 查询端口。
func provideUIStateBindingLookup(store bindingstore.Store) uistate.BindingLookup {
	if storeguard.IsNil(store) {
		return nil
	}
	return &uiStateBindingLookupAdapter{store: store}
}

// GetValue 读取偏好并复制 JSON backing array。
func (a *uiStatePreferenceStoreAdapter) GetValue(
	ctx context.Context,
	cwd, key string,
) (json.RawMessage, error) {
	value, err := a.store.GetValue(ctx, cwd, key)
	if err != nil {
		return nil, err
	}
	return cloneUIStateJSON(value), nil
}

// Upsert 逐字段转换写入参数，并复制可变 JSON。
func (a *uiStatePreferenceStoreAdapter) Upsert(ctx context.Context, params uistate.PreferenceUpsertParams) error {
	return a.store.Upsert(ctx, toStoreUIStatePreferenceUpsert(params))
}

// List 转换偏好列表，并为切片及 RawMessage 建立独立所有权。
func (a *uiStatePreferenceStoreAdapter) List(ctx context.Context, cwd string) ([]uistate.PreferenceEntry, error) {
	rows, err := a.store.List(ctx, cwd)
	if err != nil {
		return nil, err
	}
	result := make([]uistate.PreferenceEntry, len(rows))
	for index, row := range rows {
		result[index] = fromStoreUIStatePreference(row)
	}
	return result, nil
}

// Get 读取 shared file 并保持成功 nil 文件语义。
func (a *uiStateSharedFileReaderAdapter) Get(ctx context.Context, path string) (*uistate.SharedFile, error) {
	file, err := a.reader.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}
	mapped := fromStoreUIStateSharedFile(*file)
	return &mapped, nil
}

// ListAgentThreadBindings 复制 binding 列表并规范化全部八个跨边界字符串字段。
func (a *uiStateBindingLookupAdapter) ListAgentThreadBindings(ctx context.Context) ([]uistate.BindingEntry, error) {
	rows, err := a.store.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]uistate.BindingEntry, len(rows))
	for index, row := range rows {
		result[index] = fromStoreUIStateBinding(row)
	}
	return result, nil
}

func toStoreUIStatePreferenceUpsert(value uistate.PreferenceUpsertParams) uipreference.UpsertParams {
	return uipreference.UpsertParams{Cwd: value.Cwd, Key: value.Key, Value: cloneUIStateJSON(value.Value)}
}

func fromStoreUIStatePreference(value uipreference.UIPreference) uistate.PreferenceEntry {
	return uistate.PreferenceEntry{Cwd: value.Cwd, Key: value.Key, Value: cloneUIStateJSON(value.Value)}
}

func fromStoreUIStateSharedFile(value sharedfilestore.SharedFile) uistate.SharedFile {
	return uistate.SharedFile{Path: value.Path, Content: value.Content}
}

func fromStoreUIStateBinding(value bindingstore.Binding) uistate.BindingEntry {
	return uistate.BindingEntry{
		AgentID: strings.TrimSpace(value.AgentID), Provider: strings.TrimSpace(value.Provider),
		ProviderThreadID: strings.TrimSpace(value.ProviderThreadID), CodexThreadID: strings.TrimSpace(value.CodexThreadID),
		RolloutPath: strings.TrimSpace(value.RolloutPath), SessionUUID: strings.TrimSpace(value.SessionUUID),
		CodexHome: strings.TrimSpace(value.CodexHome), Cwd: strings.TrimSpace(value.Cwd),
	}
}

func cloneUIStateJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
