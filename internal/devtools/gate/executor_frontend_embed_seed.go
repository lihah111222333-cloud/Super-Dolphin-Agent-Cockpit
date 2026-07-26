package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// installExecutorSeeds 组合锁文件绑定的运行时依赖与 Go embed 编译占位种子。
func installExecutorSeeds(config executorConfig, layout executorLayout, program ExecutorProgram) error {
	if err := installRuntimeSeeds(config, layout, program); err != nil {
		return err
	}
	if !program.NeedsFrontendEmbedSeed {
		return nil
	}
	return installFrontendEmbedSeed(config, layout)
}

// installFrontendEmbedSeed 注入与产品代码无关的 Go embed 编译占位种子，不替代前端构建门禁。
func installFrontendEmbedSeed(config executorConfig, layout executorLayout) error {
	seedRoot, err := trustedDirectory(config.frontendEmbedSeedRoot, false, -1)
	if err != nil {
		return fmt.Errorf("frontend embed seed directory: %w", err)
	}
	if err := requireRegularRuntimeSeedFile(filepath.Join(seedRoot, "index.html")); err != nil {
		return fmt.Errorf("frontend embed seed index.html: %w", err)
	}
	targetRoot := filepath.Join(layout.sourceCopy, "cmd", "agent-terminal", "web-dist")
	if _, err := os.Lstat(targetRoot); err == nil {
		return errors.New("frontend embed seed target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect frontend embed seed target: %w", err)
	}
	if err := copyRuntimeSeed(seedRoot, targetRoot); err != nil {
		return fmt.Errorf("install frontend embed seed: %w", err)
	}
	return nil
}
