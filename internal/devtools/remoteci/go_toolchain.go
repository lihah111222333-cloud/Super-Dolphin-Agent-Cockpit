package remoteci

import (
	"errors"
	"fmt"
	goversion "go/version"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
	"golang.org/x/mod/modfile"
)

// ResolveGoToolchain reads the selected tree's root module without depending on
// the candidate gate implementation or the caller's checkout.
func ResolveGoToolchain(entries []sourceexport.TreeEntry) (string, error) {
	var goMod []byte
	for _, entry := range entries {
		if entry.Path != "go.mod" {
			continue
		}
		if goMod != nil {
			return "", errors.New("remote source tree contains duplicate go.mod entries")
		}
		goMod = entry.Data
	}
	if goMod == nil {
		return "", errors.New("remote source tree is missing go.mod")
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return "", fmt.Errorf("parse remote source go.mod: %w", err)
	}
	if parsed.Go == nil {
		return "", errors.New("remote source go.mod is missing the go directive")
	}
	minimum := "go" + parsed.Go.Version
	if !goversion.IsValid(minimum) {
		return "", fmt.Errorf("remote source go directive %q is invalid", parsed.Go.Version)
	}
	if parsed.Toolchain == nil || parsed.Toolchain.Name == "default" {
		return minimum, nil
	}
	if !goversion.IsValid(parsed.Toolchain.Name) {
		return "", fmt.Errorf("remote source toolchain directive %q is invalid", parsed.Toolchain.Name)
	}
	if goversion.Compare(parsed.Toolchain.Name, minimum) < 0 {
		return "", errors.New("remote source toolchain directive is older than the go directive")
	}
	return parsed.Toolchain.Name, nil
}
