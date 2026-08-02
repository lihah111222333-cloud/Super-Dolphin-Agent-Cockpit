package remoteci

// ResolveBaselineGateCompileInputs derives only the immutable compile closure
// required by the remote ECI baseline worker.
func ResolveBaselineGateCompileInputs(tree ReadOnlyGitTree, platform string) (BaselineGateCompileInputs, error) {
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return BaselineGateCompileInputs{}, err
	}
	entriesByPath, err := indexCandidateEntries(tree.Entries)
	if err != nil {
		return BaselineGateCompileInputs{}, err
	}
	manifest, err := loadBaselineBuildInputManifest(entriesByPath)
	if err != nil {
		return BaselineGateCompileInputs{}, err
	}
	_, closureByPath, err := expandInputClosure(manifest, entriesByPath)
	if err != nil {
		return BaselineGateCompileInputs{}, err
	}
	_, lockData, err := loadToolchainLock(entriesByPath, closureByPath, platform)
	if err != nil {
		return BaselineGateCompileInputs{}, err
	}
	gateSourceDigest, err := gateCompileSourceDigest(manifest, closureByPath)
	if err != nil {
		return BaselineGateCompileInputs{}, err
	}
	return BaselineGateCompileInputs{ToolchainDigest: bytesDigest(lockData), GateSourceDigest: gateSourceDigest}, nil
}
