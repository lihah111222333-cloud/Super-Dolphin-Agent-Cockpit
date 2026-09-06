package main

import "testing"

// TestStandaloneOperatorBootstrapContract 聚合来源生命周期、每请求入口和生产装配的紧密关联契约。
func TestStandaloneOperatorBootstrapContract(t *testing.T) {
	t.Run("source", TestStandaloneOperatorSourceContract)
	t.Run("tool fence", TestStandaloneOperatorToolFenceContract)
	t.Run("standalone graph starts", TestStandaloneGraphStartsWithoutHost)
	t.Run("standalone DAG execution graph", TestStandaloneGraphIncludesDAGExecution)
	t.Run("single direct peer projector", TestDirectPeerProjectorFxWiringIsSinglePerGraph)
}
