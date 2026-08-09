package cicontract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeImageCacheRefreshReceiptStrictLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	receipt := validImageCacheRefreshReceipt(now)
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeImageCacheRefreshReceipt(payload, now); err != nil {
		t.Fatalf("DecodeImageCacheRefreshReceipt() error = %v", err)
	}

	for name, mutate := range map[string]func(*ImageCacheRefreshReceipt){
		"authoritative": func(value *ImageCacheRefreshReceipt) { value.Authoritative = true },
		"SQLite writer": func(value *ImageCacheRefreshReceipt) { value.MutatesSQLite = true },
		"expired": func(value *ImageCacheRefreshReceipt) {
			value.RefreshedAtUnixSec -= 8 * 86400
			value.RefreshedAtUTC = time.Unix(value.RefreshedAtUnixSec, 0).UTC().Format(time.RFC3339)
		},
		"digest drift": func(value *ImageCacheRefreshReceipt) { value.ImageDigest = "sha256:" + strings.Repeat("f", 64) },
	} {
		changed := receipt
		mutate(&changed)
		data, marshalErr := json.Marshal(changed)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, err := DecodeImageCacheRefreshReceipt(data, now); err == nil {
			t.Fatalf("%s refresh receipt unexpectedly passed", name)
		}
	}
	if _, err := DecodeImageCacheRefreshReceipt(append(payload, []byte(` {}`)...), now); err == nil {
		t.Fatal("trailing JSON unexpectedly passed")
	}
	unknown := append(payload[:len(payload)-1], []byte(`,"unknown":true}`)...)
	if _, err := DecodeImageCacheRefreshReceipt(unknown, now); err == nil {
		t.Fatal("unknown field unexpectedly passed")
	}
}

func validImageCacheRefreshReceipt(now time.Time) ImageCacheRefreshReceipt {
	image := "172.16.26.240:5000/sdci/successor@sha256:" + strings.Repeat("a", 64)
	return ImageCacheRefreshReceipt{
		SchemaVersion: ImageCacheRefreshReceiptSchema, Action: "candidate_created_not_accepted", ExecutionProvider: ExecutionProviderID,
		RegionID: "cn-shenzhen", SourceCommit: strings.Repeat("b", 40), SourceTree: strings.Repeat("c", 40),
		BaseImage: "ghcr.io/example/base@sha256:" + strings.Repeat("d", 64), BaseSnapshotID: "s-base",
		OCIBaseImage: "ghcr.io/example/base@sha256:" + strings.Repeat("d", 64), Image: image,
		ImageDigest: "sha256:" + strings.Repeat("a", 64), ImageCacheID: "imc-refresh", ImageCacheName: "sdci-refresh",
		ImageCacheSnapshotID: "s-refresh", ImageCacheStatus: "Ready", GateBinarySHA256: "sha256:" + strings.Repeat("e", 64),
		BuilderCompileSeconds: 4, VerificationCompileSeconds: 1, RetentionDays: 7,
		RefreshedAtUnixSec: now.Unix(), RefreshedAtUTC: now.Format(time.RFC3339),
	}
}
