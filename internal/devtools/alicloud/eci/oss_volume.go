package eci

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// OSSVolume identifies a same-region OSS prefix mounted by an ECI request.
type OSSVolume struct {
	Bucket   string
	Endpoint string
	Path     string
	RoleName string
}

func ossVolumeOptions(volume OSSVolume) ([]byte, error) {
	return json.Marshal(map[string]string{
		"bucket":    volume.Bucket,
		"url":       volume.Endpoint,
		"path":      volume.Path,
		"ramRole":   volume.RoleName,
		"otherOpts": "-o max_stat_cache_size=0 -o allow_other",
	})
}

func appendOSSFlexVolume(args []string, index int, name string, options []byte) []string {
	prefix := fmt.Sprintf("--Volume.%d", index)
	return append(args,
		prefix+".Name", name,
		prefix+".Type", "FlexVolume",
		prefix+".FlexVolume.Driver", "alicloud/oss",
		prefix+".FlexVolume.Options", string(options),
	)
}

func validOSSVolume(volume OSSVolume) bool {
	return ossBucketPattern.MatchString(volume.Bucket) &&
		internalOSSEndpointPattern.MatchString(volume.Endpoint) &&
		validOSSPath(volume.Path) &&
		strings.TrimSpace(volume.RoleName) != ""
}

func validOSSPath(value string) bool {
	return value != "/" && path.IsAbs(value) && path.Clean(value) == value &&
		len(value) <= 1024 && !strings.ContainsAny(value, ":\x00")
}

var (
	ossBucketPattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	internalOSSEndpointPattern = regexp.MustCompile(`^oss-[a-z0-9-]+-internal\.aliyuncs\.com$`)
)
