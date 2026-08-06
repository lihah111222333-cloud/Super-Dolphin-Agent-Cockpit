package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"

func newRegistryParamsForTest() newRegistryParams {
	return newRegistryParams{AgentIDGenerator: idgen.NewGenerator()}
}
