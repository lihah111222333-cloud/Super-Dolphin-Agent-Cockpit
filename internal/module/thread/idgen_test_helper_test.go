package thread

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"

func newTestAgentIDGenerator() *idgen.Generator {
	return idgen.NewGenerator()
}

func normalizeStartRequestWithTestGenerator(req StartRequest) (StartRequest, string, error) {
	return normalizeStartRequest(req, idgen.NewGenerator())
}

func buildPendingSpawnRequestWithTestGenerator(row *threadConfigStoreRecord, agentID, userInputForRouter, requestCWD string) (StartRequest, error) {
	return buildPendingSpawnRequest(idgen.NewGenerator(), row, agentID, userInputForRouter, requestCWD)
}
