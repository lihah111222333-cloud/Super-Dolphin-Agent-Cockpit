package main

import (
	"errors"
	"fmt"
	goversion "go/version"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
	"golang.org/x/mod/modfile"
)

const productionGoModPath = "go.mod"

// productionGoRequirementFromEntries 从候选树自己的 go.mod 读取最低和首选工具链。
func productionGoRequirementFromEntries(entries []sourceexport.TreeEntry) (productionGoRequirement, error) {
	goMod, err := candidateProductionGoMod(entries)
	if err != nil {
		return productionGoRequirement{}, err
	}
	return parseProductionGoRequirement(goMod)
}

func candidateProductionGoMod(entries []sourceexport.TreeEntry) ([]byte, error) {
	var goMod []byte
	for _, entry := range entries {
		if entry.Path != productionGoModPath {
			continue
		}
		if goMod != nil {
			return nil, errors.New("candidate compile closure contains duplicate go.mod entries")
		}
		goMod = entry.Data
	}
	if goMod == nil {
		return nil, errors.New("candidate compile closure is missing go.mod")
	}
	return goMod, nil
}

// parseProductionGoRequirement 校验 go.mod 并归一化最低和首选 Go 版本。
func parseProductionGoRequirement(goMod []byte) (productionGoRequirement, error) {
	parsed, err := modfile.Parse(productionGoModPath, goMod, nil)
	if err != nil {
		return productionGoRequirement{}, fmt.Errorf("parse candidate go.mod: %w", err)
	}
	if parsed.Go == nil {
		return productionGoRequirement{}, errors.New("candidate go.mod is missing the go directive")
	}
	minimum := "go" + parsed.Go.Version
	if !goversion.IsValid(minimum) {
		return productionGoRequirement{}, fmt.Errorf("candidate go directive %q is invalid", parsed.Go.Version)
	}
	preferred := minimum
	if parsed.Toolchain != nil && parsed.Toolchain.Name != "default" {
		preferred = parsed.Toolchain.Name
		if !goversion.IsValid(preferred) {
			return productionGoRequirement{}, fmt.Errorf("candidate toolchain directive %q is invalid", preferred)
		}
		if goversion.Compare(preferred, minimum) < 0 {
			return productionGoRequirement{}, errors.New("candidate toolchain directive is older than the go directive")
		}
	}
	return productionGoRequirement{Minimum: minimum, Preferred: preferred}, nil
}
