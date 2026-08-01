package projectmaptrusted

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
)

// trustedGeneratorAssets 是编译进二进制、不会从候选 tree 读取的可信生成器资产容器。
//
//go:generate sh -ec "gzip -n -9 -c ../../../scripts/generate_ai_project_map.mjs > assets/generate_ai_project_map.mjs.gz.tmp; mv assets/generate_ai_project_map.mjs.gz.tmp assets/generate_ai_project_map.mjs.gz"
//go:embed assets
var trustedGeneratorAssets embed.FS

func trustedGeneratorSource() ([]byte, error) {
	gzipData, err := trustedGeneratorGzip()
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(gzipData))
	if err != nil {
		return nil, fmt.Errorf("open generator asset gzip: %w", err)
	}
	source, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read generator asset gzip: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close generator asset gzip: %w", closeErr)
	}
	return source, nil
}

func trustedGeneratorGzip() ([]byte, error) {
	data, err := trustedGeneratorAssets.ReadFile("assets/generate_ai_project_map.mjs.gz")
	if err != nil {
		return nil, fmt.Errorf("read compiled generator asset: %w", err)
	}
	return data, nil
}
