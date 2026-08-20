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
	payload := marshalRefreshReceipt(t, receipt)
	if _, err := DecodeImageCacheRefreshReceipt(payload, now); err != nil {
		t.Fatalf("DecodeImageCacheRefreshReceipt() error = %v", err)
	}
	oldReceipt := receipt
	oldReceipt.SchemaVersion = "remote-ci-imagecache-refresh-receipt/v2"
	assertRefreshReceiptRejected(t, now, "v2 refresh receipt", oldReceipt)
	missingBinary := receipt
	missingBinary.RemoteCIBinarySHA256 = ""
	assertRefreshReceiptRejected(t, now, "missing remote CI binary digest", missingBinary)

	for name, mutate := range map[string]func(*ImageCacheRefreshReceipt){
		"authoritative": func(value *ImageCacheRefreshReceipt) { value.Authoritative = true },
		"SQLite writer": func(value *ImageCacheRefreshReceipt) { value.MutatesSQLite = true },
		"expired": func(value *ImageCacheRefreshReceipt) {
			value.RefreshedAtUnixSec -= 8 * 86400
			value.RefreshedAtUTC = time.Unix(value.RefreshedAtUnixSec, 0).UTC().Format(time.RFC3339)
		},
		"image digest drift": func(value *ImageCacheRefreshReceipt) { value.ImageDigest = "sha256:" + strings.Repeat("f", 64) },
		"remote binary digest drift": func(value *ImageCacheRefreshReceipt) {
			value.RemoteCIBinarySHA256 = "sha256:" + strings.Repeat("1", 63)
		},
	} {
		changed := receipt
		mutate(&changed)
		assertRefreshReceiptRejected(t, now, name+" refresh receipt", changed)
	}
	assertRefreshPayloadRejected(t, now, "trailing JSON", append(payload, []byte(` {}`)...))
	unknown := append(payload[:len(payload)-1], []byte(`,"unknown":true}`)...)
	assertRefreshPayloadRejected(t, now, "unknown field", unknown)
}

func marshalRefreshReceipt(t *testing.T, receipt ImageCacheRefreshReceipt) []byte {
	t.Helper()
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertRefreshReceiptRejected(t *testing.T, now time.Time, name string, receipt ImageCacheRefreshReceipt) {
	t.Helper()
	assertRefreshPayloadRejected(t, now, name, marshalRefreshReceipt(t, receipt))
}

func assertRefreshPayloadRejected(t *testing.T, now time.Time, name string, payload []byte) {
	t.Helper()
	if _, err := DecodeImageCacheRefreshReceipt(payload, now); err == nil {
		t.Fatalf("%s unexpectedly passed", name)
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
		ImageCacheSnapshotID: "s-refresh", ImageCacheStatus: "Ready", GateBinarySHA256: "sha256:" + strings.Repeat("e", 64), RemoteCIBinarySHA256: "sha256:" + strings.Repeat("f", 64),
		BuilderCompileSeconds: 4, VerificationCompileSeconds: 1, RetentionDays: 7,
		RefreshedAtUnixSec: now.Unix(), RefreshedAtUTC: now.Format(time.RFC3339),
	}
}
