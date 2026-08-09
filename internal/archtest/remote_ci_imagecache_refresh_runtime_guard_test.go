package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIImageCacheRefreshRuntimeFieldChain 锁定脚本回执、严格消费、实时复核与 cache-only 请求链。
func TestRemoteCIImageCacheRefreshRuntimeFieldChain(t *testing.T) {
	root := findRepoRoot(t)
	receiptPath := filepath.Join(root, "internal", "devtools", "cicontract", "imagecache_refresh_receipt.go")
	receiptFile := parseRemoteCIContractGuardFile(t, receiptPath)
	assertRemoteCIJSONFields(t, receiptFile, "ImageCacheRefreshReceipt", [][2]string{
		{"ExecutionProvider", "execution_provider"}, {"RegionID", "region_id"},
		{"OCIBaseImage", "oci_base_image"}, {"Image", "image"},
		{"ImageCacheID", "image_cache_id"}, {"ImageCacheName", "image_cache_name"},
		{"ImageCacheSnapshotID", "image_cache_snapshot_id"}, {"ImageCacheStatus", "image_cache_status"},
		{"RetentionDays", "retention_days"}, {"MutatesSQLite", "mutates_sqlite"},
	})

	for path, markers := range map[string][]string{
		filepath.Join(root, "scripts", "refresh_remote_ci_imagecache.sh"): {
			`remote-ci-imagecache-refresh-receipt/v2`, `image_cache_name`, `region_id`,
		},
		filepath.Join(root, "cmd", "super-dolphin-gate", "remote_imagecache_refresh_runtime.go"): {
			"DecodeImageCacheRefreshReceipt", ".DescribeImageCache(", "ValidateReadyImageCache", "receipt.OCIBaseImage != state.RuntimeImage", "input.ImageCacheOnly = true",
		},
		filepath.Join(root, "internal", "devtools", "alicloud", "eci", "client.go"): {
			"if !request.ImageCacheOnly", "--ImageRegistryCredential.1.Server",
		},
	} {
		source := readRemoteCIContractGuardFile(t, path)
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing refresh runtime field-chain marker %q", path, marker)
			}
		}
	}
}
