package nativefilter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// LoadBaseConfig 读取 ~/.multi-agent/native-cli-filter.json。
//
// 行为：
//   - path 为空或文件不存在 → 返回零值 BaseConfig + nil err（fail-open，
//     允许首次运行没有 base config 文件）
//   - 文件存在但 JSON 非法 → 返回错误（让调用方决定是 log warn 还是 fail-fast）
//   - 文件存在且合法 → 解析为 BaseConfig
//
// 不做 path expansion (~/...) ；调用方负责把 ~ 展开成绝对路径。
func LoadBaseConfig(path string) (BaseConfig, error) {
	if path == "" {
		return BaseConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return BaseConfig{}, nil
		}
		return BaseConfig{}, fmt.Errorf("nativefilter: read base config %q: %w", path, err)
	}
	var cfg BaseConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return BaseConfig{}, fmt.Errorf("nativefilter: decode base config %q: %w", path, err)
	}
	return cfg, nil
}
