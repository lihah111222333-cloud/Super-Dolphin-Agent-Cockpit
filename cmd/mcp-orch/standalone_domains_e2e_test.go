//go:build e2e

package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestMCPOrchServeIndependentStateDomains 验证真实 daemon 的状态域隔离和独立生命周期。
// provider 配置仅供启动使用；本用例不调用模型，不证明 Codex/Claude 任务执行成功。
func TestMCPOrchServeIndependentStateDomains(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	fixtureA := writeServeE2EFixture(t, binary)
	fixtureB := writeServeE2EFixture(t, binary)
	if fixtureA.state == fixtureB.state || fixtureA.workspace == fixtureB.workspace || fixtureA.config == fixtureB.config {
		t.Fatal("independent fixtures share a state, workspace, or config path")
	}
	processA := startServeE2E(t, ctx, binary, fixtureA)
	processB := startServeE2E(t, ctx, binary, fixtureB)
	if processA.ready.Endpoint == processB.ready.Endpoint {
		t.Fatalf("independent state domains share endpoint %q", processA.ready.Endpoint)
	}
	if processA.ready.InstanceID == processB.ready.InstanceID {
		t.Fatalf("independent state domains share instance_id %q", processA.ready.InstanceID)
	}
	clientA := &http.Client{Timeout: 5 * time.Second}
	clientB := &http.Client{Timeout: 5 * time.Second}
	sessionA, nameA := initializeServeE2E(t, clientA, processA.ready.Endpoint, fixtureA.token)
	sessionB, nameB := initializeServeE2E(t, clientB, processB.ready.Endpoint, fixtureB.token)
	if nameA != "mcp-orch" || nameB != "mcp-orch" {
		t.Fatalf("server names=%q/%q, want mcp-orch for both domains", nameA, nameB)
	}
	// 两个 daemon 同时存活时复用请求 ID，响应必须仍属于各自会话。
	assertServeListAgents(t, clientA, processA.ready.Endpoint, fixtureA.token, sessionA, 2)
	assertServeListAgents(t, clientB, processB.ready.Endpoint, fixtureB.token, sessionB, 2)
	if err := stopServeProcessE2E(t, processA, nil); err != nil {
		t.Fatalf("state domain A shutdown: %v", err)
	}
	assertServeEndpointClosed(t, clientA, processA.ready.Endpoint)
	// A 退出后直接复用 B 的原会话，不重新 initialize 掩盖会话状态丢失。
	assertServeListAgents(t, clientB, processB.ready.Endpoint, fixtureB.token, sessionB, 3)
	if err := stopServeProcessE2E(t, processB, nil); err != nil {
		t.Fatalf("state domain B shutdown: %v", err)
	}
	assertServeEndpointClosed(t, clientB, processB.ready.Endpoint)
}
