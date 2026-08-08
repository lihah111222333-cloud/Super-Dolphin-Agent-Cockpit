package remoteci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type uncertainCreateStore struct{}

func (uncertainCreateStore) Create(context.Context, string, string) error {
	return errors.New("OSS create acknowledgement uncertain")
}

func (uncertainCreateStore) DeletePrefix(context.Context, string) error { return nil }

func TestUploadSourceAssetsRegistersObjectBeforeUncertainCreate(t *testing.T) {
	root := t.TempDir()
	bundlePath := filepath.Join(root, "source.bundle")
	manifestPath := filepath.Join(root, "source-manifest.json")
	if err := os.WriteFile(bundlePath, []byte("bundle"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("manifest"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	coordinator := &Coordinator{store: uncertainCreateStore{}}
	assets := remoteAssets{
		materialization: SourceMaterialization{BundlePath: bundlePath, ManifestPath: manifestPath},
		bundleKey:       "source-bundles/job-uncertain/bundle",
		manifestKey:     "source-bundles/job-uncertain/manifest",
	}
	var objectKeys []string
	err := coordinator.uploadSourceAssets(context.Background(), assets, &objectKeys)
	if err == nil {
		t.Fatal("uploadSourceAssets() error = nil, want uncertain create failure")
	}
	if len(objectKeys) != 1 || objectKeys[0] != assets.bundleKey {
		t.Fatalf("registered object keys = %v, want uncertain bundle key %q", objectKeys, assets.bundleKey)
	}
}
