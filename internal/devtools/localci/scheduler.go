package localci

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

const (
	maxImageBuilds  = 1
	maxGangBypasses = 1
)

type daemonIdentity struct {
	endpoint       string
	tlsFingerprint string
	daemonID       string
	ownerUID       int
	key            string
}

func newDaemonIdentity(endpoint, tlsFingerprint, daemonID string, ownerUID int) (daemonIdentity, error) {
	normalizedEndpoint, normalizedFingerprint, err := normalizeDaemonEndpoint(endpoint, tlsFingerprint)
	if err != nil {
		return daemonIdentity{}, err
	}
	daemonID = strings.TrimSpace(daemonID)
	if daemonID == "" {
		return daemonIdentity{}, errors.New("docker daemon ID is required")
	}
	if ownerUID < 0 {
		return daemonIdentity{}, fmt.Errorf("docker daemon owner UID must be non-negative: %d", ownerUID)
	}
	digest := sha256.Sum256([]byte(normalizedEndpoint + "\x00" + normalizedFingerprint + "\x00" + daemonID))
	return daemonIdentity{
		endpoint:       normalizedEndpoint,
		tlsFingerprint: normalizedFingerprint,
		daemonID:       daemonID,
		ownerUID:       ownerUID,
		key:            hex.EncodeToString(digest[:]),
	}, nil
}

func normalizeDaemonEndpoint(endpoint, tlsFingerprint string) (string, string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", "", errors.New("docker daemon endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("parse docker daemon endpoint: %w", err)
	}
	fingerprint := strings.ToLower(strings.TrimSpace(tlsFingerprint))
	switch parsed.Scheme {
	case "unix":
		return normalizeUnixEndpoint(parsed, fingerprint)
	case "tcp":
		return normalizeTCPEndpoint(parsed, fingerprint)
	default:
		return "", "", fmt.Errorf("unsupported docker daemon endpoint scheme %q", parsed.Scheme)
	}
}

// normalizeUnixEndpoint 仅规范化无 host、无 TLS 的绝对 Unix socket URI。
func normalizeUnixEndpoint(parsed *url.URL, fingerprint string) (string, string, error) {
	if parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("unix docker endpoint must contain only an absolute socket path")
	}
	if fingerprint != "" {
		return "", "", errors.New("unix docker endpoint must not declare a TLS fingerprint")
	}
	cleanPath := path.Clean(parsed.Path)
	if !strings.HasPrefix(cleanPath, "/") || cleanPath == "/" {
		return "", "", errors.New("unix docker endpoint requires an absolute socket path")
	}
	return (&url.URL{Scheme: "unix", Path: cleanPath}).String(), "", nil
}

// normalizeTCPEndpoint 要求 TCP authority 与独立 TLS 指纹同时存在。
func normalizeTCPEndpoint(parsed *url.URL, fingerprint string) (string, string, error) {
	if parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("tcp docker endpoint must contain only a host and port")
	}
	if fingerprint == "" {
		return "", "", errors.New("tcp docker endpoint requires a TLS fingerprint")
	}
	return (&url.URL{Scheme: "tcp", Host: strings.ToLower(parsed.Host)}).String(), fingerprint, nil
}

type workloadID string
type invocationID string

type workloadKind uint8

const (
	workloadBuild workloadKind = iota + 1
	workloadService
	workloadJob
)

func (k workloadKind) valid() bool {
	return k >= workloadBuild && k <= workloadJob
}

type workloadState string

const (
	stateQueued      workloadState = "queued"
	stateStarted     workloadState = "started"
	statePassed      workloadState = "passed"
	stateFailed      workloadState = "failed"
	stateInfraFailed workloadState = "infra_failed"
)

func (s workloadState) terminal() bool {
	return s == statePassed || s == stateFailed || s == stateInfraFailed
}

type workloadSpec struct {
	id           workloadID
	invocationID invocationID
	enqueueSeq   uint64
	subSeq       uint32
	kind         workloadKind
	serviceCount int
	dependencies []workloadID
}

type workloadNode struct {
	spec         workloadSpec
	state        workloadState
	gangBypasses int
}

type slotLease struct {
	id         string
	workloadID workloadID
	kind       workloadKind
}

type reservation struct {
	workloadID workloadID
	leases     []slotLease
}

type schedulerKernel struct {
	identity daemonIdentity
	nodes    map[workloadID]*workloadNode
	leases   map[string]slotLease
}

func newSchedulerKernel(identity daemonIdentity) (*schedulerKernel, error) {
	if strings.TrimSpace(identity.key) == "" || strings.TrimSpace(identity.daemonID) == "" {
		return nil, errors.New("validated daemon identity is required")
	}
	return &schedulerKernel{
		identity: identity,
		nodes:    make(map[workloadID]*workloadNode),
		leases:   make(map[string]slotLease),
	}, nil
}

// enqueue 校验 owner-local workload 并将其加入统一 FIFO。
func (s *schedulerKernel) enqueue(spec workloadSpec) error {
	if strings.TrimSpace(string(spec.id)) == "" {
		return errors.New("workload ID is required")
	}
	if strings.TrimSpace(string(spec.invocationID)) == "" {
		return errors.New("invocation ID is required")
	}
	if spec.enqueueSeq == 0 {
		return errors.New("enqueue sequence must be positive")
	}
	if !spec.kind.valid() {
		return fmt.Errorf("invalid workload kind %d", spec.kind)
	}
	if spec.serviceCount < 0 {
		return errors.New("service count must be non-negative")
	}
	if spec.kind != workloadJob && spec.serviceCount != 0 {
		return errors.New("only a job workload can own a service gang")
	}
	if 1+spec.serviceCount > maxActiveWorkloads {
		return fmt.Errorf("workload gang requires %d slots, maximum is %d", 1+spec.serviceCount, maxActiveWorkloads)
	}
	if _, exists := s.nodes[spec.id]; exists {
		return fmt.Errorf("duplicate workload ID %q", spec.id)
	}
	dependencies := append([]workloadID(nil), spec.dependencies...)
	spec.dependencies = dependencies
	s.nodes[spec.id] = &workloadNode{spec: spec, state: stateQueued}
	return nil
}

// reserveRunnable 按 runnable DAG FIFO 原子发放当前可用 lease。
func (s *schedulerKernel) reserveRunnable() ([]reservation, error) {
	if err := s.validateDAG(); err != nil {
		return nil, err
	}
	if len(s.leases) > maxActiveWorkloads {
		return nil, fmt.Errorf("active lease count %d exceeds capacity %d", len(s.leases), maxActiveWorkloads)
	}

	queued := s.sortedQueued()
	reservations := make([]reservation, 0, maxActiveWorkloads-len(s.leases))
	var blockedGang *workloadNode
	for _, node := range queued {
		decision, gang := s.decideReservation(node, blockedGang)
		blockedGang = gang
		if decision == reservationSkip {
			continue
		}
		if decision == reservationStop {
			break
		}
		reservation, err := s.reserve(node)
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, reservation)
		if node == blockedGang {
			node.gangBypasses = 0
			blockedGang = nil
		}
		if len(s.leases) == maxActiveWorkloads {
			break
		}
	}
	return reservations, nil
}

type reservationDecision uint8

const (
	reservationSkip reservationDecision = iota
	reservationStart
	reservationStop
)

// decideReservation 判断节点应跳过、启动或因 gang 防饥饿而停止扫描。
func (s *schedulerKernel) decideReservation(
	node *workloadNode,
	blockedGang *workloadNode,
) (reservationDecision, *workloadNode) {
	if !s.dependenciesSucceeded(node) {
		return reservationSkip, blockedGang
	}
	if node.spec.kind == workloadBuild && s.activeBuilds() >= maxImageBuilds {
		return reservationSkip, blockedGang
	}
	required := 1 + node.spec.serviceCount
	if required > maxActiveWorkloads-len(s.leases) {
		if node.spec.serviceCount > 0 && blockedGang == nil {
			blockedGang = node
		}
		return reservationSkip, blockedGang
	}
	if blockedGang == nil || node == blockedGang {
		return reservationStart, blockedGang
	}
	if blockedGang.gangBypasses >= maxGangBypasses {
		return reservationStop, blockedGang
	}
	blockedGang.gangBypasses++
	return reservationStart, blockedGang
}

// reserve 为一个 workload 一次性创建 primary 与全部 service lease。
func (s *schedulerKernel) reserve(node *workloadNode) (reservation, error) {
	if node.state != stateQueued {
		return reservation{}, fmt.Errorf("workload %q is not queued", node.spec.id)
	}
	required := 1 + node.spec.serviceCount
	if len(s.leases)+required > maxActiveWorkloads {
		return reservation{}, fmt.Errorf("workload %q does not fit available capacity", node.spec.id)
	}
	leases := make([]slotLease, 0, required)
	for i := 0; i < node.spec.serviceCount; i++ {
		lease := slotLease{
			id:         fmt.Sprintf("%s/service/%d", node.spec.id, i+1),
			workloadID: node.spec.id,
			kind:       workloadService,
		}
		leases = append(leases, lease)
	}
	primaryKind := node.spec.kind
	lease := slotLease{
		id:         fmt.Sprintf("%s/%d", node.spec.id, primaryKind),
		workloadID: node.spec.id,
		kind:       primaryKind,
	}
	leases = append(leases, lease)
	for _, item := range leases {
		if _, exists := s.leases[item.id]; exists {
			return reservation{}, fmt.Errorf("duplicate slot lease %q", item.id)
		}
	}
	for _, item := range leases {
		s.leases[item.id] = item
	}
	node.state = stateStarted
	return reservation{workloadID: node.spec.id, leases: leases}, nil
}

// complete 只接受已启动 workload 的终态，并在零修改检查后归还全部 slot。
func (s *schedulerKernel) complete(id workloadID, terminalState workloadState) error {
	node, exists := s.nodes[id]
	if !exists {
		return fmt.Errorf("unknown workload %q", id)
	}
	if node.state != stateStarted {
		return fmt.Errorf("workload %q is not running", id)
	}
	if !terminalState.terminal() {
		return fmt.Errorf("workload %q completion state %q is not terminal", id, terminalState)
	}
	leaseIDs := make([]string, 0, 1+node.spec.serviceCount)
	for leaseID, lease := range s.leases {
		if lease.workloadID == id {
			leaseIDs = append(leaseIDs, leaseID)
		}
	}
	if len(leaseIDs) != 1+node.spec.serviceCount {
		return fmt.Errorf(
			"workload %q lease count %d does not match expected %d",
			id,
			len(leaseIDs),
			1+node.spec.serviceCount,
		)
	}
	for _, leaseID := range leaseIDs {
		delete(s.leases, leaseID)
	}
	node.state = terminalState
	return nil
}

func (s *schedulerKernel) state(id workloadID) workloadState {
	node, exists := s.nodes[id]
	if !exists {
		return ""
	}
	return node.state
}

func (s *schedulerKernel) activeBuilds() int {
	count := 0
	for _, lease := range s.leases {
		if lease.kind == workloadBuild {
			count++
		}
	}
	return count
}

func (s *schedulerKernel) dependenciesSucceeded(node *workloadNode) bool {
	for _, dependencyID := range node.spec.dependencies {
		if s.nodes[dependencyID].state != statePassed {
			return false
		}
	}
	return true
}

// sortedQueued 生成稳定的 build、service gang、job FIFO 视图。
func (s *schedulerKernel) sortedQueued() []*workloadNode {
	queued := make([]*workloadNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		if node.state == stateQueued {
			queued = append(queued, node)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		left := queued[i].spec
		right := queued[j].spec
		if left.enqueueSeq != right.enqueueSeq {
			return left.enqueueSeq < right.enqueueSeq
		}
		leftRank := workloadRank(left)
		rightRank := workloadRank(right)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if left.subSeq != right.subSeq {
			return left.subSeq < right.subSeq
		}
		return left.id < right.id
	})
	return queued
}

func workloadRank(spec workloadSpec) int {
	if spec.kind == workloadBuild {
		return 0
	}
	if spec.kind == workloadService || spec.serviceCount > 0 {
		return 1
	}
	return 2
}

// validateDAG 在每次发放 lease 前拒绝未知依赖、重复边和依赖环。
func (s *schedulerKernel) validateDAG() error {
	if err := s.validateDependencies(); err != nil {
		return err
	}
	return s.detectDependencyCycle()
}

// validateDependencies 校验依赖目标存在且每条边只登记一次。
func (s *schedulerKernel) validateDependencies() error {
	for id, node := range s.nodes {
		seen := make(map[workloadID]struct{}, len(node.spec.dependencies))
		for _, dependencyID := range node.spec.dependencies {
			if dependencyID == id {
				return fmt.Errorf("workload %q depends on itself", id)
			}
			if _, exists := s.nodes[dependencyID]; !exists {
				return fmt.Errorf("workload %q has unknown dependency %q", id, dependencyID)
			}
			if _, duplicate := seen[dependencyID]; duplicate {
				return fmt.Errorf("workload %q repeats dependency %q", id, dependencyID)
			}
			seen[dependencyID] = struct{}{}
		}
	}
	return nil
}

// detectDependencyCycle 对 owner-local DAG 执行深度优先环检测。
func (s *schedulerKernel) detectDependencyCycle() error {
	visiting := make(map[workloadID]bool, len(s.nodes))
	visited := make(map[workloadID]bool, len(s.nodes))
	var visit func(workloadID) error
	visit = func(id workloadID) error {
		if visiting[id] {
			return fmt.Errorf("dependency cycle includes workload %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependencyID := range s.nodes[id].spec.dependencies {
			if err := visit(dependencyID); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range s.nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
