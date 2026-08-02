package eci

import "strings"

type invalidCreateRequestCase struct {
	name   string
	mutate func(*CreateRequest)
}

func invalidCreateRequestCases() []invalidCreateRequestCase {
	return []invalidCreateRequestCase{
		{"invalid group name", func(request *CreateRequest) { request.ContainerGroupName = "Shard" }},
		{"invalid resources", func(request *CreateRequest) { request.Resources = Resources{CPU: 4, MemoryGiB: 20} }},
		{"missing task command", func(request *CreateRequest) { request.Command = nil }},
		{"invalid task environment name", func(request *CreateRequest) { request.Environment = map[string]string{"1INVALID": "value"} }},
		{"long init environment value", func(request *CreateRequest) {
			request.InitContainer.Environment = map[string]string{"VALID": strings.Repeat("x", 257)}
		}},
		{"duplicate volume name", func(request *CreateRequest) { request.WorkVolume.Name = request.SourceVolume.Name }},
		{"missing task mount", func(request *CreateRequest) { request.MainVolumeMounts = request.MainVolumeMounts[:1] }},
		{"unknown task volume", func(request *CreateRequest) { request.MainVolumeMounts[0].Name = "other-data" }},
	}
}
