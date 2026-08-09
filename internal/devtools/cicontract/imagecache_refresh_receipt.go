package cicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const ImageCacheRefreshReceiptSchema = "remote-ci-imagecache-refresh-receipt/v2"

var refreshGitObjectPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ImageCacheRefreshReceipt 描述 OSS 中由仓库刷新脚本写入的有限期 ECI ImageCache 物料。
type ImageCacheRefreshReceipt struct {
	SchemaVersion              string `json:"schema_version"`
	Authoritative              bool   `json:"authoritative"`
	Action                     string `json:"action"`
	ExecutionProvider          string `json:"execution_provider"`
	RegionID                   string `json:"region_id"`
	SourceCommit               string `json:"source_commit"`
	SourceTree                 string `json:"source_tree"`
	BaseImage                  string `json:"base_image"`
	BaseSnapshotID             string `json:"base_snapshot_id"`
	OCIBaseImage               string `json:"oci_base_image"`
	Image                      string `json:"image"`
	ImageDigest                string `json:"image_digest"`
	ImageCacheID               string `json:"image_cache_id"`
	ImageCacheName             string `json:"image_cache_name"`
	ImageCacheSnapshotID       string `json:"image_cache_snapshot_id"`
	ImageCacheStatus           string `json:"image_cache_status"`
	GateBinarySHA256           string `json:"gate_binary_sha256"`
	BuilderCompileSeconds      int64  `json:"builder_compile_seconds"`
	VerificationCompileSeconds int64  `json:"verification_compile_seconds"`
	RetentionDays              int64  `json:"retention_days"`
	RefreshedAtUnixSec         int64  `json:"refreshed_at_unix_sec"`
	RefreshedAtUTC             string `json:"refreshed_at_utc"`
	MutatesSQLite              bool   `json:"mutates_sqlite"`
}

// Validate 校验刷新物料的无权威语义、不可变身份和有限生命周期。
func (receipt ImageCacheRefreshReceipt) Validate(now time.Time) error {
	for _, validate := range []func() error{
		receipt.validateHeader, receipt.validateSource, receipt.validateImages,
		receipt.validateCacheIdentity, receipt.validateTiming,
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	return receipt.validateLifecycle(now)
}

// validateHeader 拒绝权威、SQLite 写入或非阿里云 ECI 的刷新回执头。
func (receipt ImageCacheRefreshReceipt) validateHeader() error {
	if receipt.SchemaVersion != ImageCacheRefreshReceiptSchema || receipt.Authoritative || receipt.MutatesSQLite || receipt.Action != "candidate_created_not_accepted" {
		return errors.New("remote ImageCache refresh receipt header is invalid")
	}
	if receipt.ExecutionProvider != ExecutionProviderID || strings.TrimSpace(receipt.RegionID) == "" {
		return errors.New("remote ImageCache refresh receipt cloud identity is invalid")
	}
	return nil
}

func (receipt ImageCacheRefreshReceipt) validateSource() error {
	if !refreshGitObjectPattern.MatchString(receipt.SourceCommit) || !refreshGitObjectPattern.MatchString(receipt.SourceTree) {
		return errors.New("remote ImageCache refresh receipt source identity is invalid")
	}
	return nil
}

// validateImages 绑定三个不可变镜像引用以及缓存镜像自身摘要。
func (receipt ImageCacheRefreshReceipt) validateImages() error {
	for _, image := range []string{receipt.BaseImage, receipt.OCIBaseImage, receipt.Image} {
		if !validGenerationOneOCIImage(image) {
			return fmt.Errorf("remote ImageCache refresh receipt image %q is not immutable", image)
		}
	}
	if !isCanonicalSHA256(receipt.ImageDigest) || !strings.HasSuffix(receipt.Image, "@"+receipt.ImageDigest) || !isCanonicalSHA256(receipt.GateBinarySHA256) {
		return errors.New("remote ImageCache refresh receipt digest binding is invalid")
	}
	return nil
}

func (receipt ImageCacheRefreshReceipt) validateCacheIdentity() error {
	if strings.TrimSpace(receipt.ImageCacheID) == "" || strings.TrimSpace(receipt.ImageCacheName) == "" || strings.TrimSpace(receipt.ImageCacheSnapshotID) == "" || receipt.ImageCacheStatus != "Ready" {
		return errors.New("remote ImageCache refresh receipt Ready identity is incomplete")
	}
	return nil
}

func (receipt ImageCacheRefreshReceipt) validateTiming() error {
	if receipt.BuilderCompileSeconds < 0 || receipt.VerificationCompileSeconds < 0 || receipt.RetentionDays < 1 || receipt.RetentionDays > 30 {
		return errors.New("remote ImageCache refresh receipt timing or retention is invalid")
	}
	return nil
}

func (receipt ImageCacheRefreshReceipt) validateLifecycle(now time.Time) error {
	refreshedAt, err := time.Parse(time.RFC3339, receipt.RefreshedAtUTC)
	if err != nil || refreshedAt.Unix() != receipt.RefreshedAtUnixSec {
		return errors.New("remote ImageCache refresh receipt timestamp is invalid")
	}
	now = now.UTC()
	if refreshedAt.After(now.Add(5*time.Minute)) || !now.Before(refreshedAt.Add(time.Duration(receipt.RetentionDays)*24*time.Hour)) {
		return errors.New("remote ImageCache refresh receipt is future-dated or expired")
	}
	return nil
}

// DecodeImageCacheRefreshReceipt 严格解码单个 JSON 回执并拒绝未知字段与尾随数据。
func DecodeImageCacheRefreshReceipt(payload []byte, now time.Time) (ImageCacheRefreshReceipt, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var receipt ImageCacheRefreshReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return ImageCacheRefreshReceipt{}, fmt.Errorf("decode remote ImageCache refresh receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ImageCacheRefreshReceipt{}, errors.New("decode remote ImageCache refresh receipt: trailing JSON value")
	}
	if err := receipt.Validate(now); err != nil {
		return ImageCacheRefreshReceipt{}, err
	}
	return receipt, nil
}
