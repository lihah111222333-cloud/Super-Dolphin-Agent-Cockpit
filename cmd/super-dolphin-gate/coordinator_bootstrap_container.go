package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type productionBootstrapContainerInspect struct {
	ID     string   `json:"Id"`
	Image  string   `json:"Image"`
	Path   string   `json:"Path"`
	Args   []string `json:"Args"`
	Config *struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	HostConfig *struct {
		AutoRemove     bool     `json:"AutoRemove"`
		ReadonlyRootfs bool     `json:"ReadonlyRootfs"`
		NanoCPUs       int64    `json:"NanoCpus"`
		Memory         int64    `json:"Memory"`
		NetworkMode    string   `json:"NetworkMode"`
		CapDrop        []string `json:"CapDrop"`
		SecurityOpt    []string `json:"SecurityOpt"`
		Binds          []string `json:"Binds"`
	} `json:"HostConfig"`
	State *struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
	} `json:"State"`
}

// VerifyAndRemoveContainer 观察 inspect/log，验证后无条件删除一次性容器。
func (productionBootstrapHostRuntime) VerifyAndRemoveContainer(
	ctx context.Context,
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	attestation productionBootstrapAttestation,
) (retErr error) {
	defer func() {
		retErr = errors.Join(retErr, removeProductionBootstrapContainer(context.WithoutCancel(ctx), attestation.ContainerID))
	}()
	inspectData, err := productionBootstrapDocker(ctx, "inspect", attestation.ContainerID)
	if err != nil {
		return err
	}
	document, err := decodeProductionBootstrapContainerInspect(inspectData)
	if err != nil {
		return err
	}
	canonicalInspect, err := json.Marshal(document)
	if err != nil {
		return err
	}
	inspectDigest := sha256.Sum256(canonicalInspect)
	if "sha256:"+hex.EncodeToString(inspectDigest[:]) != attestation.ContainerInspectDigest {
		return errors.New("production bootstrap container inspect digest drifted")
	}
	logs, err := productionBootstrapDocker(ctx, "logs", attestation.ContainerID)
	if err != nil {
		return err
	}
	logDigest := sha256.Sum256(logs)
	if "sha256:"+hex.EncodeToString(logDigest[:]) != attestation.ContainerLogDigest {
		return errors.New("production bootstrap container log digest drifted")
	}
	return validateProductionBootstrapContainer(config, root, request, attestation, document)
}

func decodeProductionBootstrapContainerInspect(data []byte) (productionBootstrapContainerInspect, error) {
	var documents []productionBootstrapContainerInspect
	if err := json.Unmarshal(data, &documents); err != nil || len(documents) != 1 {
		return productionBootstrapContainerInspect{}, errors.Join(errors.New("production bootstrap container inspect must contain one document"), err)
	}
	return documents[0], nil
}

// validateProductionBootstrapContainerHost 固定资源、网络和 Linux capability 策略。
func validateProductionBootstrapContainerHost(host *struct {
	AutoRemove     bool     `json:"AutoRemove"`
	ReadonlyRootfs bool     `json:"ReadonlyRootfs"`
	NanoCPUs       int64    `json:"NanoCpus"`
	Memory         int64    `json:"Memory"`
	NetworkMode    string   `json:"NetworkMode"`
	CapDrop        []string `json:"CapDrop"`
	SecurityOpt    []string `json:"SecurityOpt"`
	Binds          []string `json:"Binds"`
}) error {
	if host.AutoRemove || !host.ReadonlyRootfs || host.NanoCPUs != productionBootstrapContainerCPU ||
		host.Memory != productionBootstrapContainerMemory || host.NetworkMode != "bridge" ||
		!slices.Contains(host.CapDrop, "ALL") || !productionBootstrapHasNoNewPrivileges(host.SecurityOpt) {
		return errors.New("production bootstrap container host policy drifted")
	}
	return nil
}

func validateProductionBootstrapContainerState(document productionBootstrapContainerInspect) error {
	if document.State.Running || document.State.Status != "exited" || document.State.ExitCode != 0 {
		return errors.New("production bootstrap container did not exit successfully")
	}
	return nil
}

func productionBootstrapHasNoNewPrivileges(options []string) bool {
	for _, option := range options {
		if option == "no-new-privileges" || option == "no-new-privileges:true" {
			return true
		}
	}
	return false
}

// validateProductionBootstrapContainerBinds 只允许可选 Docker socket，拒绝源码挂载。
func validateProductionBootstrapContainerBinds(config productionCoordinatorConfig, binds []string) error {
	if len(binds) > 1 {
		return errors.New("production bootstrap container has unexpected bind mounts")
	}
	if len(binds) == 0 {
		return nil
	}
	parts := strings.Split(binds[0], ":")
	if len(parts) < 2 || parts[len(parts)-1] == "ro" {
		return errors.New("production bootstrap Docker socket bind is invalid")
	}
	source, destination := parts[0], parts[1]
	if destination != "/var/run/docker.sock" || filepath.Base(source) != "docker.sock" || !filepath.IsAbs(source) {
		return errors.New("production bootstrap container bind is not the Docker socket")
	}
	for _, root := range []string{config.TrustedRepository, config.TrustedSourceRoot, config.CandidateBuildRoot} {
		if productionPathContains(root, source) {
			return errors.New("production bootstrap Docker socket bind traverses a production source root")
		}
	}
	return nil
}
