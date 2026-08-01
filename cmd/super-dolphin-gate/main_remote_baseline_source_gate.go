package main

import (
	"errors"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteGateSource 构建并校验提交、树或推送范围的权威 gate source。
func remoteGateSource(options remoteRunOptions, profile gatecontract.Profile, objectFormat, commit, tree, base string) (gatecontract.SourceSpec, error) {
	if options.Tree != "" {
		return remoteTreeGateSource(options, profile, objectFormat, tree, base)
	}
	if profile == gatecontract.ProfilePush {
		return remotePushGateSource(options, objectFormat, commit, tree, base)
	}
	return remoteCommitGateSource(options, objectFormat, commit, tree)
}

// remoteTreeGateSource 构建显式树 source 并拒绝推送专用参数。
func remoteTreeGateSource(options remoteRunOptions, profile gatecontract.Profile, objectFormat, tree, base string) (gatecontract.SourceSpec, error) {
	if profile == gatecontract.ProfilePush {
		return gatecontract.SourceSpec{}, errors.New("push profile requires a range source")
	}
	if hasRemotePushSourceFlags(options) {
		return gatecontract.SourceSpec{}, errors.New("push source flags are only valid with profile push")
	}
	source := gatecontract.SourceSpec{Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormat(objectFormat), Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: base}, SourceTreeSHA: tree}
	return source, source.Validate()
}

// remoteCommitGateSource 构建普通提交 source 并拒绝推送专用参数。
func remoteCommitGateSource(options remoteRunOptions, objectFormat, commit, tree string) (gatecontract.SourceSpec, error) {
	if hasRemotePushSourceFlags(options) {
		return gatecontract.SourceSpec{}, errors.New("push source flags are only valid with profile push")
	}
	source := gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormat(objectFormat), Commit: &gatecontract.CommitSource{SHA: commit}, SourceTreeSHA: tree}
	return source, source.Validate()
}

// remotePushGateSource 构建推送范围 source 并区分新建 ref 的空树基线。
func remotePushGateSource(options remoteRunOptions, objectFormat, commit, tree, base string) (gatecontract.SourceSpec, error) {
	if missingRemotePushSourceFlags(options) {
		return gatecontract.SourceSpec{}, errors.New("profile push requires --local-ref, --remote-ref, --observed-remote, and --update-kind")
	}
	baseKind, baseSHA := remotePushBase(options.UpdateKind, base)
	source := gatecontract.SourceSpec{Kind: gatecontract.SourceKindRange, ObjectFormat: gatecontract.GitObjectFormat(objectFormat), Range: &gatecontract.RangeSource{BaseKind: baseKind, BaseSHA: baseSHA, HeadSHA: commit, LocalRef: options.LocalRef, RemoteRef: options.RemoteRef, ObservedRemoteSHA: options.ObservedRemote, UpdateKind: gatecontract.UpdateKind(options.UpdateKind)}, SourceTreeSHA: tree}
	return source, source.Validate()
}

// remotePushBase 选择新建 ref 与普通更新的基线身份。
func remotePushBase(updateKind, base string) (gatecontract.BaseKind, string) {
	if gatecontract.UpdateKind(updateKind) == gatecontract.UpdateKindCreate {
		return gatecontract.BaseKindEmptyTree, ""
	}
	return gatecontract.BaseKindCommit, base
}

// hasRemotePushSourceFlags 判断任一推送身份字段是否被设置。
func hasRemotePushSourceFlags(options remoteRunOptions) bool {
	return options.LocalRef != "" || options.RemoteRef != "" || options.ObservedRemote != "" || options.UpdateKind != ""
}

// missingRemotePushSourceFlags 判断推送身份字段是否存在空白或缺失。
func missingRemotePushSourceFlags(options remoteRunOptions) bool {
	return strings.TrimSpace(options.LocalRef) == "" || strings.TrimSpace(options.RemoteRef) == "" || strings.TrimSpace(options.ObservedRemote) == "" || strings.TrimSpace(options.UpdateKind) == ""
}
