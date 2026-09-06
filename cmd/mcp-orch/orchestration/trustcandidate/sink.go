package trustcandidate

import "fmt"

// AdmitSink 只计算 sink decision；不调用网络、文件、导航、tool 或 A2A。
func AdmitSink(envelope TrustEnvelope, sink SinkKind) (SinkDecision, error) {
	return AdmitSinkWithConsent(envelope, sink, UserConsent{})
}

// AdmitSinkWithConsent 记录显式同意输入但仍保持 candidate-only、零副作用。
func AdmitSinkWithConsent(envelope TrustEnvelope, sink SinkKind, consent UserConsent) (SinkDecision, error) {
	if err := envelope.Validate(); err != nil {
		return SinkDecision{}, err
	}
	if !knownSink(sink) {
		return SinkDecision{}, fmt.Errorf("%w: %q", ErrUnknownSink, sink)
	}
	decision := newSinkDecision(envelope, sink)
	classifySink(&decision, envelope)
	applyConsentNote(&decision, consent)
	if err := decision.Validate(); err != nil {
		return SinkDecision{}, err
	}
	return decision, nil
}

// newSinkDecision 创建默认 deny 判定，计数初始严格为零。
func newSinkDecision(envelope TrustEnvelope, sink SinkKind) SinkDecision {
	return SinkDecision{
		SchemaVersion: SchemaVersion, EnvelopeDigest: envelope.EnvelopeDigest, SinkKind: sink,
		Decision: DecisionDenied, Reason: "candidate-only sink admission has no external effect implementation",
	}
}

// classifySink 根据内容和 sink kind 计算 inert/allowed quoted 判定，不执行外部调用。
func classifySink(decision *SinkDecision, envelope TrustEnvelope) {
	if inertContentOrSink(envelope.ContentKind, decision.SinkKind) {
		decision.Decision = DecisionInert
		decision.Inert = true
		decision.Reason = "link/image/UI/navigation/network/file content remains inert without an executing sink and receipt"
		return
	}
	if decision.SinkKind == SinkModelUserData && envelope.ContentKind != ContentControl {
		if err := envelope.ValidateForQuote(); err == nil {
			decision.Decision = DecisionAllowedQuoted
			decision.Allowed = true
			decision.Reason = "quoted user/data role is allowed as a non-executing candidate"
		}
	}
}

// inertContentOrSink 判断 link/image 或主动 sink 是否必须保持 inert。
func inertContentOrSink(kind ContentKind, sink SinkKind) bool {
	if kind == ContentLink || kind == ContentImage {
		return true
	}
	switch sink {
	case SinkUILinkImageMedia, SinkUIHTMLMarkdown, SinkUIANSIOSC, SinkNavigation, SinkNetwork, SinkFile:
		return true
	default:
		return false
	}
}

// applyConsentNote 只记录候选同意说明，不能生成 network/navigation receipt。
func applyConsentNote(decision *SinkDecision, consent UserConsent) {
	if consent.Explicit && consent.ConsentID != "" && decision.Inert {
		decision.Reason = "explicit consent is recorded but candidate package cannot execute or issue a sink receipt"
	}
}
