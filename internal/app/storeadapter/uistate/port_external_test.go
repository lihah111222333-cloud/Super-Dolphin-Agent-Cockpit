package uistateadapter_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
)

type externalUIStatePorts struct{}

func (externalUIStatePorts) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

func (externalUIStatePorts) Upsert(context.Context, uistate.PreferenceUpsertParams) error {
	return nil
}

func (externalUIStatePorts) List(context.Context, string) ([]uistate.PreferenceEntry, error) {
	return nil, nil
}

func (externalUIStatePorts) Get(context.Context, string) (*uistate.SharedFile, error) {
	return nil, nil
}

func (externalUIStatePorts) ListAgentThreadBindings(context.Context) ([]uistate.BindingEntry, error) {
	return nil, nil
}

// TestUIStatePersistencePortsAreExternallyImplementable 固定 App 只能依赖 uistate 自有端口和 DTO。
func TestUIStatePersistencePortsAreExternallyImplementable(t *testing.T) {
	t.Parallel()
	var ports externalUIStatePorts
	var _ uistate.PreferenceReader = ports
	var _ uistate.PreferenceStore = ports
	var _ uistate.SharedFileReader = ports
	var _ uistate.BindingLookup = ports
	_ = uistate.PreferenceUpsertParams{}
	_ = uistate.PreferenceEntry{}
	_ = uistate.SharedFile{}
	_ = uistate.BindingEntry{}
}
