package gate

import "testing"

func TestValidateRemoteGoDistributionPlatformAcceptsOnlyLinuxAMD64(t *testing.T) {
	if err := ValidateRemoteGoDistributionPlatform("linux", "amd64"); err != nil {
		t.Fatalf("ValidateRemoteGoDistributionPlatform(linux/amd64): %v", err)
	}
	for _, platform := range [][2]string{{"darwin", "arm64"}, {"linux", "arm64"}, {"windows", "amd64"}} {
		if err := ValidateRemoteGoDistributionPlatform(platform[0], platform[1]); err == nil {
			t.Fatalf("ValidateRemoteGoDistributionPlatform(%s/%s) unexpectedly passed", platform[0], platform[1])
		}
	}
}
