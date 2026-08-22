package remoteportable

import (
	"errors"
)

const PortableWorkloadManifestMaxBytes = 1 << 20

// Profile is the provider-neutral profile wire value.
type Profile string

const (
	// ProfileLocalFast identifies local-fast planning.
	ProfileLocalFast Profile = "local-fast"
	// ProfilePush identifies push planning.
	ProfilePush Profile = "push"
	// ProfileRemoteRequired identifies remote-required planning.
	ProfileRemoteRequired Profile = "remote-required"
	// ProfilePromotion identifies promotion planning.
	ProfilePromotion Profile = "promotion"
	// ProfileRelease identifies release planning.
	ProfileRelease Profile = "release"
)

// Validate rejects profiles outside the portable wire contract.
func (profile Profile) Validate() error {
	switch profile {
	case ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion, ProfileRelease:
		return nil
	default:
		return errors.New("unsupported portable profile")
	}
}

// PortableGoWorkloadInventory contains exact-tree Go package targets.
type PortableGoWorkloadInventory struct {
	GoPackages      []string
	NestedGoModules []string
}
