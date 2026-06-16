package multilsp

import (
	"context"
	"encoding/json"
	"strings"
)

// configurationRequestHandlerFromInitOptions 从init选项处理configuration请求处理器。
func configurationRequestHandlerFromInitOptions(initOptions map[string]any) ServerRequestHandler {
	settings, ok := initOptions["settings"].(map[string]any)
	if !ok || len(settings) == 0 {
		return nil
	}
	settings = cloneAnyMap(settings)
	return func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != LSPCompatMethodWorkspaceConfiguration {
			return nil, ErrMethodNotSupported
		}
		var request struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		result := make([]any, len(request.Items))
		for index, item := range request.Items {
			section := strings.TrimSpace(item.Section)
			if section == "" {
				result[index] = cloneAnyMap(settings)
				continue
			}
			if value, ok := settings[section]; ok {
				result[index] = value
			}
		}
		return result, nil
	}
}
