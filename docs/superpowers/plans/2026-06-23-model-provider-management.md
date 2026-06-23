# Model Provider Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a model provider management module for OpenRouter, DeepSeek, and Qwen with backend RPCs, redacted env-key status, and a React settings UI.

**Architecture:** Keep v1 inside the existing UI preference boundary. Backend RPCs live in `internal/module/uistate`, store registry JSON under `settings.modelProviders.registry`, and apply a selected vendor by writing existing Codex preference keys. The frontend adds a focused settings card using the existing backend API facade and current `Panel`/form styles.

**Tech Stack:** Go `uistate` RPC handlers, existing `uipreference` store through `Service.SetPreference`, React/Vite settings page, Vitest/React Testing Library.

---

## File Structure

- Create `internal/module/uistate/model_providers.go`: registry types, default vendors, validation, env status, and apply helpers.
- Create `internal/module/uistate/model_providers_test.go`: backend RPC behavior tests.
- Modify `internal/module/uistate/rpc.go`: register `modelProviders/list`, `modelProviders/save`, and `modelProviders/apply`.
- Modify `frontend-app/src/shared/api/backendApi.js`: add RPC constants and facade methods.
- Modify `frontend-app/src/shared/api/backendApi.test.js`: verify facade payloads.
- Modify `frontend-app/src/pages/settings/services/settingsPageService.js`: re-export model provider facade methods.
- Create `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`: self-contained UI state and form.
- Modify `frontend-app/src/pages/settings/components/SettingsPageComponents.css`: small layout styles for the card.
- Modify `frontend-app/src/pages/settings/SettingsPage.jsx`: import and render the card.
- Modify `frontend-app/src/pages/settings/SettingsPage.test.jsx`: cover list, save, missing env, and apply success.
- Modify `frontend-app/src/shared/i18n/appI18n.js`: add text copy under `settings.modelProviders`.

## Task 1: Backend Registry And RPCs

**Files:**
- Create: `internal/module/uistate/model_providers.go`
- Create: `internal/module/uistate/model_providers_test.go`
- Modify: `internal/module/uistate/rpc.go`

- [ ] **Step 1: Write backend RPC tests**

Create `internal/module/uistate/model_providers_test.go` with these tests:

```go
package uistate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func newModelProviderTestServer(t *testing.T) *rpc.Server {
	t.Helper()
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	return server
}

func TestModelProvidersListReturnsDefaultTemplatesAndEnvStatus(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-openrouter-secret")
	server := newModelProviderTestServer(t)

	result, err := server.Dispatch(context.Background(), "modelProviders/list", json.RawMessage(`{"cwd":"/repo/app"}`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/list) error = %v", err)
	}
	if strings.Contains(string(result), "sk-openrouter-secret") {
		t.Fatalf("modelProviders/list leaked api key: %s", result)
	}

	var got modelProviderRegistry
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", result, err)
	}
	if len(got.Vendors) != 3 {
		t.Fatalf("len(vendors) = %d, want 3", len(got.Vendors))
	}
	if got.Vendors[0].ID != "openrouter" || !got.Vendors[0].Configured || got.Vendors[0].MaskedEnv == "" {
		t.Fatalf("openrouter status = %#v, want configured with masked env", got.Vendors[0])
	}
}

func TestModelProvidersSaveRejectsInvalidRegistry(t *testing.T) {
	server := newModelProviderTestServer(t)

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
		"cwd":"/repo/app",
		"registry":{"vendors":[{"id":"bad","label":"Bad","enabled":true,"baseURL":"ftp://bad","envKey":"bad-key","codexModelProvider":"bad","defaultModel":"bad","tokenPool":{"fallbackVendorId":"missing"}}]}
	}`))
	if err == nil {
		t.Fatal("Dispatch(modelProviders/save) error = nil, want validation failure")
	}
}

func TestModelProvidersApplyWritesCodexPreferences(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek-secret")
	server := newModelProviderTestServer(t)

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
		"cwd":"/repo/app",
		"registry":{"vendors":[{"id":"deepseek","label":"DeepSeek","enabled":true,"baseURL":"https://api.deepseek.com/v1","envKey":"DEEPSEEK_API_KEY","codexModelProvider":"deepseek","defaultModel":"deepseek-chat","codexHome":"/tmp/codex","codexInstanceKey":"work"}]}
	}`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/save) error = %v", err)
	}
	if _, err := server.Dispatch(context.Background(), "modelProviders/apply", json.RawMessage(`{"cwd":"/repo/app","vendorId":"deepseek"}`)); err != nil {
		t.Fatalf("Dispatch(modelProviders/apply) error = %v", err)
	}

	assertPreference := func(key, want string) {
		t.Helper()
		result, err := server.Dispatch(context.Background(), "ui/preferences/get", json.RawMessage(`{"cwd":"/repo/app","key":"`+key+`"}`))
		if err != nil {
			t.Fatalf("Dispatch(ui/preferences/get %s) error = %v", key, err)
		}
		var got string
		if err := json.Unmarshal(result, &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", result, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	assertPreference("settings.provider.codex.codexModelProvider", "deepseek")
	assertPreference("settings.provider.codex.codexHome", "/tmp/codex")
	assertPreference("settings.provider.codex.codexInstanceKey", "work")
}

func TestModelProvidersApplyRejectsMissingEnv(t *testing.T) {
	t.Setenv("QWEN_API_KEY", "")
	server := newModelProviderTestServer(t)

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
		"cwd":"/repo/app",
		"registry":{"vendors":[{"id":"qwen","label":"Qwen","enabled":true,"baseURL":"https://dashscope.aliyuncs.com/compatible-mode/v1","envKey":"QWEN_API_KEY","codexModelProvider":"qwen","defaultModel":"qwen-plus"}]}
	}`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/save) error = %v", err)
	}
	_, err := server.Dispatch(context.Background(), "modelProviders/apply", json.RawMessage(`{"cwd":"/repo/app","vendorId":"qwen"}`))
	if err == nil || !strings.Contains(err.Error(), "environment variable QWEN_API_KEY is not configured") {
		t.Fatalf("Dispatch(modelProviders/apply) error = %v, want missing env", err)
	}
}
```

- [ ] **Step 2: Run backend tests and verify they fail**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/uistate -run TestModelProviders -count=1
```

Expected: FAIL because `modelProviderRegistry` and the `modelProviders/*` RPC methods do not exist.

- [ ] **Step 3: Add registry implementation**

Create `internal/module/uistate/model_providers.go` with these exact responsibilities:

```go
package uistate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const preferenceModelProviderRegistry = "settings.modelProviders.registry"

var modelProviderEnvKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type modelProvidersParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type modelProvidersSaveParams struct {
	Cwd      string                `json:"cwd,omitempty"`
	Registry modelProviderRegistry `json:"registry"`
}

type modelProvidersApplyParams struct {
	Cwd      string `json:"cwd,omitempty"`
	VendorID string `json:"vendorId"`
}

type modelProviderRegistry struct {
	Vendors        []modelProviderVendor `json:"vendors"`
	ActiveVendorID string                `json:"activeVendorId,omitempty"`
}

type modelProviderVendor struct {
	ID                 string                 `json:"id"`
	Label              string                 `json:"label"`
	Enabled            bool                   `json:"enabled"`
	BaseURL            string                 `json:"baseURL"`
	EnvKey             string                 `json:"envKey"`
	CodexModelProvider string                 `json:"codexModelProvider"`
	DefaultModel        string                 `json:"defaultModel"`
	CodexHome           string                 `json:"codexHome,omitempty"`
	CodexInstanceKey    string                 `json:"codexInstanceKey,omitempty"`
	Budget              modelProviderBudget    `json:"budget,omitempty"`
	TokenPool           modelProviderTokenPool `json:"tokenPool,omitempty"`
	Configured          bool                   `json:"configured,omitempty"`
	MaskedEnv           string                 `json:"maskedEnv,omitempty"`
	EnvStatus           string                 `json:"envStatus,omitempty"`
}

type modelProviderBudget struct {
	DailyUSD   float64 `json:"dailyUsd,omitempty"`
	MonthlyUSD float64 `json:"monthlyUsd,omitempty"`
}

type modelProviderTokenPool struct {
	Priority         float64 `json:"priority,omitempty"`
	FallbackVendorID string  `json:"fallbackVendorId,omitempty"`
}
```

Add default vendors:

```go
func defaultModelProviderRegistry() modelProviderRegistry {
	return modelProviderRegistry{Vendors: []modelProviderVendor{
		{ID: "openrouter", Label: "OpenRouter", Enabled: true, BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENROUTER_API_KEY", CodexModelProvider: "openrouter", DefaultModel: "openai/gpt-4.1", TokenPool: modelProviderTokenPool{Priority: 10, FallbackVendorID: "deepseek"}},
		{ID: "deepseek", Label: "DeepSeek", Enabled: false, BaseURL: "https://api.deepseek.com/v1", EnvKey: "DEEPSEEK_API_KEY", CodexModelProvider: "deepseek", DefaultModel: "deepseek-chat", TokenPool: modelProviderTokenPool{Priority: 20, FallbackVendorID: "qwen"}},
		{ID: "qwen", Label: "Qwen", Enabled: false, BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", EnvKey: "QWEN_API_KEY", CodexModelProvider: "qwen", DefaultModel: "qwen-plus", TokenPool: modelProviderTokenPool{Priority: 30}},
	}}
}
```

Add helpers named exactly:

```go
func listModelProviders(ctx context.Context, svc Service, cwd string) (modelProviderRegistry, error)
func saveModelProviders(ctx context.Context, svc Service, p modelProvidersSaveParams) (map[string]any, error)
func applyModelProvider(ctx context.Context, svc Service, p modelProvidersApplyParams) (modelProviderRegistry, error)
func modelProviderRegistryFromPreference(value any) (modelProviderRegistry, error)
func validateModelProviderRegistry(registry modelProviderRegistry) error
func maskModelProviderEnv(value string) string
func withModelProviderEnvStatus(registry modelProviderRegistry) modelProviderRegistry
```

Implementation rules:

- `listModelProviders` calls `svc.GetPreferences(withPreferenceScope(ctx, cwd))`, reads `preferenceValue(*prefs, preferenceModelProviderRegistry)`, falls back to `defaultModelProviderRegistry()`, validates, then returns `withModelProviderEnvStatus(registry)`.
- `saveModelProviders` validates `p.Registry`, stores it with `svc.SetPreference(withPreferenceScope(ctx, p.Cwd), preferenceModelProviderRegistry, p.Registry)`, then returns `map[string]any{"ok": true}`.
- `applyModelProvider` loads the registry, validates it, finds `p.VendorID`, rejects disabled vendors, rejects missing env, writes `settings.provider.codex.codexModelProvider`, optionally writes `settings.provider.codex.codexHome` and `settings.provider.codex.codexInstanceKey` when non-empty, updates `ActiveVendorID`, stores the registry, and returns `withModelProviderEnvStatus(registry)`.
- `validateModelProviderRegistry` checks required strings, absolute HTTP(S) URL, env key regex, non-negative budget/token pool numbers, duplicate ids, and existing fallback ids.

- [ ] **Step 4: Register RPC handlers**

Modify `internal/module/uistate/rpc.go` inside `NewUIStateHandlers`:

```go
"modelProviders/list": platformrpc.StrictHandler(func(ctx context.Context, p modelProvidersParams) (any, error) {
	return listModelProviders(ctx, svc, p.Cwd)
}),
"modelProviders/save": platformrpc.StrictHandler(func(ctx context.Context, p modelProvidersSaveParams) (any, error) {
	return saveModelProviders(ctx, svc, p)
}),
"modelProviders/apply": platformrpc.StrictHandler(func(ctx context.Context, p modelProvidersApplyParams) (any, error) {
	return applyModelProvider(ctx, svc, p)
}),
```

Place the handlers near `ui/preferences/*` because they are preference-backed settings.

- [ ] **Step 5: Run backend tests and guard**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/uistate -run TestModelProviders -count=1
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/uistate/model_providers.go
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/uistate/rpc.go
```

Expected: PASS.

- [ ] **Step 6: Commit backend changes**

Run:

```powershell
git add -- internal/module/uistate/model_providers.go internal/module/uistate/model_providers_test.go internal/module/uistate/rpc.go
git commit -m "feat: 增加模型厂商管理 RPC"
```

## Task 2: Frontend Backend API Facade

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/pages/settings/services/settingsPageService.js`

- [ ] **Step 1: Write facade tests**

Add this test to `frontend-app/src/shared/api/backendApi.test.js`:

```js
it('exposes model provider management RPC facade methods', async () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });
  const registry = { vendors: [{ id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1' }] };

  await api.listModelProviders({ cwd: '/repo/app' });
  await api.saveModelProviders({ cwd: '/repo/app', registry });
  await api.applyModelProvider({ cwd: '/repo/app', vendorId: 'openrouter' });

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MODEL_PROVIDERS_LIST, { cwd: '/repo/app' });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MODEL_PROVIDERS_SAVE, { cwd: '/repo/app', registry });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MODEL_PROVIDERS_APPLY, { cwd: '/repo/app', vendorId: 'openrouter' });
});
```

- [ ] **Step 2: Run facade test and verify it fails**

Run:

```powershell
cd frontend-app
npm test -- backendApi.test.js -t "model provider management"
```

Expected: FAIL because facade methods are missing.

- [ ] **Step 3: Add facade constants and methods**

Modify `RPC_METHODS` in `frontend-app/src/shared/api/backendApi.js`:

```js
MODEL_PROVIDERS_LIST: 'modelProviders/list',
MODEL_PROVIDERS_SAVE: 'modelProviders/save',
MODEL_PROVIDERS_APPLY: 'modelProviders/apply',
```

Add these methods to `createConfigProjectApi`:

```js
listModelProviders: (params = {}) => callBackend(RPC_METHODS.MODEL_PROVIDERS_LIST, assertPlainObject(RPC_METHODS.MODEL_PROVIDERS_LIST, params)),
saveModelProviders: (params) => {
  const payload = assertPlainObject(RPC_METHODS.MODEL_PROVIDERS_SAVE, params);
  if (!payload.registry || typeof payload.registry !== 'object' || Array.isArray(payload.registry)) {
    throw new Error(`${RPC_METHODS.MODEL_PROVIDERS_SAVE}: registry is required`);
  }
  return callBackend(RPC_METHODS.MODEL_PROVIDERS_SAVE, payload);
},
applyModelProvider: (params) => callBackend(
  RPC_METHODS.MODEL_PROVIDERS_APPLY,
  requireKey(RPC_METHODS.MODEL_PROVIDERS_APPLY, assertPlainObject(RPC_METHODS.MODEL_PROVIDERS_APPLY, params), 'vendorId'),
),
```

Export the new facade methods near `getPreference` and `setPreference`:

```js
export const listModelProviders = backendApi.listModelProviders;
export const saveModelProviders = backendApi.saveModelProviders;
export const applyModelProvider = backendApi.applyModelProvider;
```

- [ ] **Step 4: Re-export methods for SettingsPage**

Modify `frontend-app/src/pages/settings/services/settingsPageService.js` imports:

```js
  applyModelProvider as applyModelProviderBackend,
  listModelProviders as listModelProvidersBackend,
  saveModelProviders as saveModelProvidersBackend,
```

Add exports:

```js
export function applyModelProvider(payload) {
  return applyModelProviderBackend(payload);
}

export function listModelProviders(payload) {
  return listModelProvidersBackend(payload);
}

export function saveModelProviders(payload) {
  return saveModelProvidersBackend(payload);
}
```

- [ ] **Step 5: Run facade tests**

Run:

```powershell
cd frontend-app
npm test -- backendApi.test.js -t "model provider management"
```

Expected: PASS.

- [ ] **Step 6: Commit facade changes**

Run:

```powershell
git add -- frontend-app/src/shared/api/backendApi.js frontend-app/src/shared/api/backendApi.test.js frontend-app/src/pages/settings/services/settingsPageService.js
git commit -m "feat: 增加模型厂商前端 RPC facade"
```

## Task 3: Settings UI Card

**Files:**
- Create: `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`
- Modify: `frontend-app/src/pages/settings/components/SettingsPageComponents.css`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Modify: `frontend-app/src/shared/i18n/appI18n.js`
- Modify: `frontend-app/src/pages/settings/SettingsPage.test.jsx`

- [ ] **Step 1: Add UI tests**

Extend the `backend` hoisted mock in `frontend-app/src/pages/settings/SettingsPage.test.jsx`:

```js
  applyModelProvider: vi.fn(),
  listModelProviders: vi.fn(),
  saveModelProviders: vi.fn(),
```

Add default mock setup in `beforeEach`:

```js
  backend.listModelProviders.mockResolvedValue({
    activeVendorId: '',
    vendors: [
      { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1', configured: true, maskedEnv: 'sk**********uter', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: 'deepseek' } },
      { id: 'deepseek', label: 'DeepSeek', enabled: false, baseURL: 'https://api.deepseek.com/v1', envKey: 'DEEPSEEK_API_KEY', codexModelProvider: 'deepseek', defaultModel: 'deepseek-chat', configured: false, maskedEnv: '', envStatus: 'missing', budget: {}, tokenPool: { priority: 20, fallbackVendorId: 'qwen' } },
      { id: 'qwen', label: 'Qwen', enabled: false, baseURL: 'https://dashscope.aliyuncs.com/compatible-mode/v1', envKey: 'QWEN_API_KEY', codexModelProvider: 'qwen', defaultModel: 'qwen-plus', configured: false, maskedEnv: '', envStatus: 'missing', budget: {}, tokenPool: { priority: 30 } },
    ],
  });
  backend.saveModelProviders.mockResolvedValue({ ok: true });
  backend.applyModelProvider.mockResolvedValue({
    activeVendorId: 'openrouter',
    vendors: [
      { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1', configured: true, maskedEnv: 'sk**********uter', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: 'deepseek' } },
    ],
  });
```

Add tests:

```js
describe('SettingsPage model provider management', () => {
  it('renders model vendors with redacted API key status', async () => {
    renderSettingsPage();

    const card = await screen.findByTestId('settings-model-providers-card');
    expect(card).toHaveTextContent('Model Providers');
    expect(card).toHaveTextContent('OpenRouter');
    expect(card).toHaveTextContent('OPENROUTER_API_KEY');
    expect(card).toHaveTextContent('configured');
    expect(card).not.toHaveTextContent('sk-openrouter-secret');
  });

  it('saves the edited vendor registry through the facade', async () => {
    renderSettingsPage();

    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.change(within(card).getByLabelText('Default Model'), { target: { value: 'openai/gpt-4.1-mini' } });
    fireEvent.click(within(card).getByRole('button', { name: '保存厂商配置' }));

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        registry: expect.objectContaining({
          vendors: expect.arrayContaining([expect.objectContaining({ id: 'openrouter', defaultModel: 'openai/gpt-4.1-mini' })]),
        }),
      }));
    });
  });

  it('shows missing env status without API key input fields', async () => {
    renderSettingsPage();

    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.click(within(card).getByRole('button', { name: /DeepSeek/ }));

    expect(within(card).getByText('missing')).toBeInTheDocument();
    expect(within(card).queryByLabelText('API Key')).not.toBeInTheDocument();
  });

  it('applies a configured vendor and refreshes active state', async () => {
    renderSettingsPage();

    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.click(within(card).getByRole('button', { name: '应用厂商' }));

    await waitFor(() => {
      expect(backend.applyModelProvider).toHaveBeenCalledWith({ cwd: '/repo/app', vendorId: 'openrouter' });
      expect(card).toHaveTextContent('已应用 OpenRouter');
    });
  });
});
```

- [ ] **Step 2: Run UI tests and verify they fail**

Run:

```powershell
cd frontend-app
npm test -- SettingsPage.test.jsx -t "model provider management"
```

Expected: FAIL because the card and facade imports are not wired.

- [ ] **Step 3: Add text copy**

Modify `frontend-app/src/shared/i18n/appI18n.js` under each `settings` copy object:

```js
modelProviders: {
  title: 'Model Providers',
  empty: '没有可用厂商',
  configured: 'configured',
  missing: 'missing',
  enabled: 'enabled',
  disabled: 'disabled',
  active: 'active',
  save: '保存厂商配置',
  apply: '应用厂商',
  refresh: '刷新状态',
  saved: '厂商配置已保存',
  appliedPrefix: '已应用 ',
  loadFailed: '读取模型厂商失败：',
  saveFailed: '保存模型厂商失败：',
  applyFailed: '应用模型厂商失败：',
  labels: {
    enabled: 'Enabled',
    baseURL: 'Base URL',
    envKey: 'Environment Variable',
    codexModelProvider: 'Codex Model Provider',
    defaultModel: 'Default Model',
    codexHome: 'Codex Home',
    codexInstanceKey: 'Instance Key',
    dailyUsd: 'Daily Budget USD',
    monthlyUsd: 'Monthly Budget USD',
    priority: 'Token Pool Priority',
    fallbackVendorId: 'Fallback Vendor',
  },
},
```

- [ ] **Step 4: Create the card component**

Create `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`:

```jsx
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Panel } from '../../shared/pageComponents.jsx';
import { applyModelProvider, listModelProviders, saveModelProviders } from '../services/settingsPageService.js';

const EMPTY_REGISTRY = Object.freeze({ vendors: [], activeVendorId: '' });

function ModelProvidersCard({ copy, cwd }) {
  const modelCopy = copy.modelProviders;
  const [registry, setRegistry] = useState(EMPTY_REGISTRY);
  const [selectedId, setSelectedId] = useState('');
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const [busy, setBusy] = useState(false);
  const selectedVendor = useMemo(() => registry.vendors.find((item) => item.id === selectedId) || registry.vendors[0] || null, [registry, selectedId]);

  const load = useCallback(async () => {
    if (!cwd) return;
    setBusy(true);
    try {
      const next = normalizeRegistry(await listModelProviders({ cwd }));
      setRegistry(next);
      setSelectedId((current) => next.vendors.some((item) => item.id === current) ? current : next.vendors[0]?.id || '');
      setNotice({ level: 'info', message: '' });
    } catch (error) {
      setNotice({ level: 'error', message: modelCopy.loadFailed + (error?.message || error) });
    } finally {
      setBusy(false);
    }
  }, [cwd, modelCopy]);

  useEffect(() => { void load(); }, [load]);

  const updateVendor = useCallback((key, value) => {
    setRegistry((current) => ({
      ...current,
      vendors: current.vendors.map((vendor) => vendor.id === selectedVendor?.id ? vendorWithUpdate(vendor, key, value) : vendor),
    }));
  }, [selectedVendor]);

  const save = useCallback(async () => {
    if (!cwd) return;
    setBusy(true);
    try {
      await saveModelProviders({ cwd, registry: registryForSave(registry) });
      setNotice({ level: 'info', message: modelCopy.saved });
    } catch (error) {
      setNotice({ level: 'error', message: modelCopy.saveFailed + (error?.message || error) });
    } finally {
      setBusy(false);
    }
  }, [cwd, modelCopy, registry]);

  const apply = useCallback(async () => {
    if (!cwd || !selectedVendor) return;
    setBusy(true);
    try {
      const next = normalizeRegistry(await applyModelProvider({ cwd, vendorId: selectedVendor.id }));
      setRegistry(next);
      setSelectedId(selectedVendor.id);
      setNotice({ level: 'info', message: modelCopy.appliedPrefix + selectedVendor.label });
    } catch (error) {
      setNotice({ level: 'error', message: modelCopy.applyFailed + (error?.message || error) });
    } finally {
      setBusy(false);
    }
  }, [cwd, modelCopy, selectedVendor]);

  return (
    <Panel title={modelCopy.title}>
      <section className="settings-model-providers" data-testid="settings-model-providers-card">
        <div className="settings-model-provider-list">
          {registry.vendors.map((vendor) => (
            <button key={vendor.id} type="button" className={vendor.id === selectedVendor?.id ? 'is-selected' : ''} onClick={() => setSelectedId(vendor.id)}>
              <strong>{vendor.label}</strong>
              <span>{vendorStatusText(vendor, registry.activeVendorId, modelCopy)}</span>
            </button>
          ))}
        </div>
        {selectedVendor ? <ModelProviderForm copy={modelCopy} busy={busy} registry={registry} vendor={selectedVendor} onApply={apply} onSave={save} onUpdate={updateVendor} /> : <p>{modelCopy.empty}</p>}
        {notice.message ? <p className={notice.level === 'error' ? 'danger-text' : 'settings-provider-note'} role={notice.level === 'error' ? 'alert' : 'status'}>{notice.message}</p> : null}
      </section>
    </Panel>
  );
}

function ModelProviderForm({ busy, copy, registry, vendor, onApply, onSave, onUpdate }) {
  return (
    <div className="settings-model-provider-detail">
      <div className="form-grid">
        <label className="checkbox-line"><input type="checkbox" checked={vendor.enabled} onChange={(event) => onUpdate('enabled', event.target.checked)} /> {copy.labels.enabled}</label>
        <label>{copy.labels.baseURL}<input aria-label="Base URL" value={vendor.baseURL} onChange={(event) => onUpdate('baseURL', event.target.value)} /></label>
        <label>{copy.labels.envKey}<input aria-label="Environment Variable" value={vendor.envKey} onChange={(event) => onUpdate('envKey', event.target.value)} /></label>
        <label>{copy.labels.codexModelProvider}<input aria-label="Codex Model Provider" value={vendor.codexModelProvider} onChange={(event) => onUpdate('codexModelProvider', event.target.value)} /></label>
        <label>{copy.labels.defaultModel}<input aria-label="Default Model" value={vendor.defaultModel} onChange={(event) => onUpdate('defaultModel', event.target.value)} /></label>
        <label>{copy.labels.codexHome}<input aria-label="Model Provider Codex Home" value={vendor.codexHome || ''} onChange={(event) => onUpdate('codexHome', event.target.value)} /></label>
        <label>{copy.labels.codexInstanceKey}<input aria-label="Model Provider Instance Key" value={vendor.codexInstanceKey || ''} onChange={(event) => onUpdate('codexInstanceKey', event.target.value)} /></label>
        <label>{copy.labels.dailyUsd}<input aria-label="Daily Budget USD" type="number" min="0" value={vendor.budget?.dailyUsd ?? ''} onChange={(event) => onUpdate('budget.dailyUsd', event.target.value)} /></label>
        <label>{copy.labels.monthlyUsd}<input aria-label="Monthly Budget USD" type="number" min="0" value={vendor.budget?.monthlyUsd ?? ''} onChange={(event) => onUpdate('budget.monthlyUsd', event.target.value)} /></label>
        <label>{copy.labels.priority}<input aria-label="Token Pool Priority" type="number" min="0" value={vendor.tokenPool?.priority ?? ''} onChange={(event) => onUpdate('tokenPool.priority', event.target.value)} /></label>
        <label>{copy.labels.fallbackVendorId}<select aria-label="Fallback Vendor" value={vendor.tokenPool?.fallbackVendorId || ''} onChange={(event) => onUpdate('tokenPool.fallbackVendorId', event.target.value)}><option value=""></option>{registry.vendors.filter((item) => item.id !== vendor.id).map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}</select></label>
      </div>
      <div className="settings-actions">
        <button className="btn btn-secondary" type="button" onClick={onSave} disabled={busy}>{copy.save}</button>
        <button className="btn btn-primary" type="button" onClick={onApply} disabled={busy || !vendor.configured}>{copy.apply}</button>
      </div>
    </div>
  );
}
```

Also add helpers in the same file and export the component:

```jsx
function normalizeRegistry(payload) {
  const vendors = Array.isArray(payload?.vendors) ? payload.vendors.map(normalizeVendor) : [];
  return { vendors, activeVendorId: textValue(payload?.activeVendorId) };
}

function normalizeVendor(vendor) {
  return {
    id: textValue(vendor.id),
    label: textValue(vendor.label || vendor.id),
    enabled: Boolean(vendor.enabled),
    baseURL: textValue(vendor.baseURL),
    envKey: textValue(vendor.envKey),
    codexModelProvider: textValue(vendor.codexModelProvider),
    defaultModel: textValue(vendor.defaultModel),
    codexHome: textValue(vendor.codexHome),
    codexInstanceKey: textValue(vendor.codexInstanceKey),
    configured: Boolean(vendor.configured),
    maskedEnv: textValue(vendor.maskedEnv),
    envStatus: textValue(vendor.envStatus),
    budget: vendor.budget && typeof vendor.budget === 'object' ? vendor.budget : {},
    tokenPool: vendor.tokenPool && typeof vendor.tokenPool === 'object' ? vendor.tokenPool : {},
  };
}

function vendorWithUpdate(vendor, key, value) {
  if (key === 'budget.dailyUsd' || key === 'budget.monthlyUsd') {
    return { ...vendor, budget: { ...(vendor.budget || {}), [key.split('.')[1]]: numberOrEmpty(value) } };
  }
  if (key === 'tokenPool.priority' || key === 'tokenPool.fallbackVendorId') {
    return { ...vendor, tokenPool: { ...(vendor.tokenPool || {}), [key.split('.')[1]]: key.endsWith('priority') ? numberOrEmpty(value) : value } };
  }
  return { ...vendor, [key]: value };
}

function registryForSave(registry) {
  return {
    activeVendorId: registry.activeVendorId,
    vendors: registry.vendors.map(storedVendor),
  };
}

function storedVendor(vendor) {
  const out = { ...vendor };
  delete out.configured;
  delete out.maskedEnv;
  delete out.envStatus;
  out.budget = {
    dailyUsd: numericOrZero(vendor.budget?.dailyUsd),
    monthlyUsd: numericOrZero(vendor.budget?.monthlyUsd),
  };
  out.tokenPool = {
    priority: numericOrZero(vendor.tokenPool?.priority),
    fallbackVendorId: textValue(vendor.tokenPool?.fallbackVendorId),
  };
  return out;
}

function vendorStatusText(vendor, activeVendorId, copy) {
  return [vendor.id === activeVendorId ? copy.active : '', vendor.enabled ? copy.enabled : copy.disabled, vendor.configured ? copy.configured : copy.missing].filter(Boolean).join(' / ');
}

function numericOrZero(value) {
  const number = Number(value);
  return Number.isFinite(number) && number >= 0 ? number : 0;
}

function numberOrEmpty(value) {
  if (value === '') return '';
  const number = Number(value);
  return Number.isFinite(number) ? number : '';
}

function textValue(value) {
  return (value || '').toString();
}

export { ModelProvidersCard };
```

- [ ] **Step 5: Wire card into SettingsPage**

Modify imports in `frontend-app/src/pages/settings/SettingsPage.jsx`:

```js
import { ModelProvidersCard } from './components/ModelProvidersCard.jsx';
```

Render it after `ProviderPropertiesCard`:

```jsx
<ModelProvidersCard copy={copy} cwd={cwd} />
```

- [ ] **Step 6: Add minimal styles**

Append to `frontend-app/src/pages/settings/components/SettingsPageComponents.css`:

```css
.settings-model-providers {
  display: grid;
  gap: 16px;
}

.settings-model-provider-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
}

.settings-model-provider-list button {
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface-2);
  color: var(--text-pri);
  padding: 10px;
  text-align: left;
}

.settings-model-provider-list button.is-selected {
  border-color: var(--accent);
}

.settings-model-provider-list span {
  display: block;
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 4px;
}

.settings-model-provider-detail {
  min-width: 0;
}

.settings-provider-note {
  color: var(--text-muted);
}
```

- [ ] **Step 7: Run UI tests**

Run:

```powershell
cd frontend-app
npm test -- SettingsPage.test.jsx -t "model provider management"
```

Expected: PASS.

- [ ] **Step 8: Commit UI changes**

Run:

```powershell
git add -- frontend-app/src/pages/settings/components/ModelProvidersCard.jsx frontend-app/src/pages/settings/components/SettingsPageComponents.css frontend-app/src/pages/settings/SettingsPage.jsx frontend-app/src/shared/i18n/appI18n.js frontend-app/src/pages/settings/SettingsPage.test.jsx
git commit -m "feat: 增加模型厂商设置页"
```

## Task 4: Verification And Integration

**Files:**
- Verify all changed files from Tasks 1-3.

- [ ] **Step 1: Run backend verification**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/uistate -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend verification**

Run:

```powershell
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands PASS.

- [ ] **Step 3: Review owned diff and log**

Run:

```powershell
git status --short --branch
git log --oneline --decorate --graph codex/integration-20260623..HEAD
git diff codex/integration-20260623...HEAD --stat
```

Expected: only model provider backend, settings UI, API facade, tests, and approved docs/plan changes appear.

- [ ] **Step 4: Merge task branch back to integration branch**

Run:

```powershell
git switch codex/integration-20260623
git merge --no-ff codex/model-provider-management
```

Expected: merge commit succeeds without conflicts.

- [ ] **Step 5: Verify after merge**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/uistate -count=1
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands PASS.

- [ ] **Step 6: Report final status**

Run:

```powershell
git status --short --branch
git log --oneline --decorate --graph origin/main..HEAD
```

Expected: integration branch contains the spec commit, plan commit if committed, backend commit, facade commit, UI commit, and merge commit; working tree is clean.
