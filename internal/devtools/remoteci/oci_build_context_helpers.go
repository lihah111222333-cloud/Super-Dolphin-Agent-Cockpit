package remoteci

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

func runtimeDepsBuildArguments(lock toolchainLock) ([]BuildArgument, error) {
	arguments := make([]BuildArgument, 0, 6)
	for _, image := range lock.BaseImages {
		if image.Name == "GO_IMAGE" || image.Name == "NODE_IMAGE" {
			arguments = append(arguments, BuildArgument{Name: image.Name, Value: image.Reference})
		}
	}
	if len(arguments) != 2 {
		return nil, errors.New("runtime dependencies require locked GO_IMAGE and NODE_IMAGE")
	}
	for _, artifact := range lock.RuntimeTools.SqruffArtifacts {
		architecture := "AMD64"
		if artifact.Platform == "linux/arm64" {
			architecture = "ARM64"
		}
		arguments = append(arguments, BuildArgument{Name: "SQRUFF_ARCHIVE_SHA256_" + architecture, Value: artifact.SHA256}, BuildArgument{Name: "SQRUFF_ARCHIVE_URL_" + architecture, Value: artifact.URL})
	}
	sort.Slice(arguments, func(left, right int) bool { return arguments[left].Name < arguments[right].Name })
	return arguments, nil
}

func validateAcceptedCandidateDigests(request CandidateRequest) error {
	for _, digest := range [][2]string{{"accepted input digest", request.AcceptedInputDigest}, {"accepted policy digest", request.AcceptedPolicyDigest}, {"accepted image digest", request.AcceptedImageDigest}, {"accepted config digest", request.AcceptedConfigDigest}} {
		if err := validateDigest(digest[0], digest[1]); err != nil {
			return err
		}
	}
	return nil
}

func canonicalCopyPath(source string) (string, error) {
	cleaned := strings.TrimSuffix(source, "/")
	if source == "" || source == "." || path.IsAbs(source) || path.Clean(source) != cleaned {
		return "", errors.New("path is not canonical")
	}
	return cleaned, nil
}

func validateLockedImageArgumentDefaults(lines []string, locked map[string]string) error {
	declared := make(map[string]struct{}, len(locked))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		instruction := fields[0]
		if strings.EqualFold(instruction, "FROM") {
			break
		}
		if !strings.EqualFold(instruction, "ARG") {
			continue
		}
		name, value, hasDefault := strings.Cut(strings.TrimSpace(trimmed[len(instruction):]), "=")
		expected, isLocked := locked[name]
		if !isLocked {
			continue
		}
		if !hasDefault || value == "" {
			return fmt.Errorf("Dockerfile ARG %q must default to its toolchain lock value", name)
		}
		if value != expected {
			return fmt.Errorf("Dockerfile ARG %q default does not match the toolchain lock", name)
		}
		if _, duplicate := declared[name]; duplicate {
			return fmt.Errorf("Dockerfile ARG %q is declared more than once before FROM", name)
		}
		declared[name] = struct{}{}
	}
	for name := range locked {
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("Dockerfile must declare locked ARG %q with a default before FROM", name)
		}
	}
	return nil
}
