package admissionproof

import "context"

// UnavailableContentResolver 表示当前没有可用的CONTENT owner。
type UnavailableContentResolver struct{}

// ResolveContentSnapshot 明确返回CONTENT owner unavailable，不创建local proof。
func (UnavailableContentResolver) ResolveContentSnapshot(context.Context, ContentResolveKey) (ContentSnapshot, error) {
	return ContentSnapshot{}, ErrContentOwnerUnavailable
}

// RejectAllContentVerifier 阻断任何未由真实CONTENT authority产生的snapshot。
type RejectAllContentVerifier struct{}

// VerifyContentSnapshot 拒绝任何未由真实CONTENT owner回验的snapshot。
func (RejectAllContentVerifier) VerifyContentSnapshot(context.Context, ContentResolveKey, ContentSnapshot) error {
	return ErrContentOwnerUnavailable
}

// UnavailableResourceResolver 表示当前没有可用的RESOURCE owner。
type UnavailableResourceResolver struct{}

// ResolveResourceSnapshot 明确返回RESOURCE owner unavailable，不创建默认reservation。
func (UnavailableResourceResolver) ResolveResourceSnapshot(context.Context, ResourceResolveKey) (ResourceSnapshot, error) {
	return ResourceSnapshot{}, ErrResourceOwnerUnavailable
}

// RejectAllResourceVerifier 阻断任何未由真实RESOURCE authority产生的snapshot。
type RejectAllResourceVerifier struct{}

// VerifyResourceSnapshot 拒绝任何未由真实RESOURCE owner回验的snapshot。
func (RejectAllResourceVerifier) VerifyResourceSnapshot(context.Context, ResourceResolveKey, ResourceSnapshot) error {
	return ErrResourceOwnerUnavailable
}

var _ ContentResolver = UnavailableContentResolver{}
var _ ContentVerifier = RejectAllContentVerifier{}
var _ ResourceResolver = UnavailableResourceResolver{}
var _ ResourceVerifier = RejectAllResourceVerifier{}
