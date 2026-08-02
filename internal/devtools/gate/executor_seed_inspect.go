package gate

import (
	"errors"
	"io"
)

// executeRuntimeSeedInspectCommand 只输出重算后的运行时内容清单，供增量刷新诊断字段漂移。
func executeRuntimeSeedInspectCommand(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: super-dolphin-gate worker runtime-seed-inspect <snapshot-root> <runtime-root>")
	}
	manifest, err := BuildRuntimeSeedManifest(args[0], args[1])
	if err != nil {
		return err
	}
	return EncodeRuntimeSeedManifest(stdout, manifest)
}
