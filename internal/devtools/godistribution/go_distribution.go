// Package godistribution owns the immutable official Go distribution lock.
package godistribution

import (
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Version is the only Go release accepted by this repository.
	Version = "go1.26.5"

	remoteGOOS   = "linux"
	remoteGOARCH = "amd64"
)

// Asset is one official Go archive with its complete integrity identity.
type Asset struct {
	Version string
	GOOS    string
	GOARCH  string
	URL     string
	SHA256  string
	Size    int64
}

//go:embed go-distribution.lock
var lockedAssets string

// Lookup returns the exact official archive locked for one GOOS/GOARCH pair.
func Lookup(goos, goarch string) (Asset, error) {
	assets, err := parse(lockedAssets)
	if err != nil {
		return Asset{}, err
	}
	asset, ok := assets[goos+"/"+goarch]
	if !ok {
		return Asset{}, fmt.Errorf("no locked Go distribution for %s/%s", goos, goarch)
	}
	return asset, nil
}

// RemoteCIAsset returns the only archive permitted for remote CI.
func RemoteCIAsset() (Asset, error) {
	return Lookup(remoteGOOS, remoteGOARCH)
}

// ValidateRemoteCIAsset rejects every archive except the exact linux/amd64 lock item.
func ValidateRemoteCIAsset(asset Asset) error {
	expected, err := RemoteCIAsset()
	if err != nil {
		return fmt.Errorf("load remote CI Go distribution: %w", err)
	}
	if asset != expected {
		return errors.New("remote CI must use the exact locked Go linux/amd64 distribution")
	}
	return nil
}

func parse(data string) (map[string]Asset, error) {
	assets := make(map[string]Asset, 3)
	for lineNumber, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			return nil, fmt.Errorf("Go distribution lock line %d must contain six tab-separated fields", lineNumber+1)
		}
		size, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil || size <= 0 {
			return nil, fmt.Errorf("Go distribution lock line %d has invalid byte size", lineNumber+1)
		}
		asset := Asset{Version: fields[0], GOOS: fields[1], GOARCH: fields[2], URL: fields[3], SHA256: fields[4], Size: size}
		if err := validateAsset(asset); err != nil {
			return nil, fmt.Errorf("Go distribution lock line %d: %w", lineNumber+1, err)
		}
		key := asset.GOOS + "/" + asset.GOARCH
		if _, duplicate := assets[key]; duplicate {
			return nil, fmt.Errorf("Go distribution lock duplicates %s", key)
		}
		assets[key] = asset
	}
	for _, key := range []string{"darwin/arm64", "darwin/amd64", "linux/amd64"} {
		if _, ok := assets[key]; !ok {
			return nil, fmt.Errorf("Go distribution lock is missing %s", key)
		}
	}
	if len(assets) != 3 {
		return nil, errors.New("Go distribution lock contains unsupported platforms")
	}
	return assets, nil
}

func validateAsset(asset Asset) error {
	if asset.Version != Version {
		return fmt.Errorf("version must be %q", Version)
	}
	if asset.GOOS != "darwin" && asset.GOOS != "linux" {
		return fmt.Errorf("unsupported GOOS %q", asset.GOOS)
	}
	if asset.GOARCH != "amd64" && asset.GOARCH != "arm64" {
		return fmt.Errorf("unsupported GOARCH %q", asset.GOARCH)
	}
	if asset.GOOS == "linux" && asset.GOARCH != "amd64" {
		return fmt.Errorf("unsupported platform %s/%s", asset.GOOS, asset.GOARCH)
	}
	expectedURL := fmt.Sprintf("https://go.dev/dl/%s.%s-%s.tar.gz", Version, asset.GOOS, asset.GOARCH)
	if asset.URL != expectedURL {
		return fmt.Errorf("URL must be the official archive URL %q", expectedURL)
	}
	if len(asset.SHA256) != 64 || strings.Trim(asset.SHA256, "0123456789abcdef") != "" {
		return errors.New("SHA-256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}
