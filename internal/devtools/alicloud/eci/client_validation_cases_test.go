package eci

import "strings"

type invalidCreateRequestCase struct {
	name   string
	mutate func(*CreateRequest)
}

func invalidCreateRequestCases() []invalidCreateRequestCase {
	return []invalidCreateRequestCase{
		{"missing image cache snapshot", func(request *CreateRequest) { request.ImageCacheSnapshotID = "" }},
		{"invalid group name", func(request *CreateRequest) { request.ContainerGroupName = "Shard" }},
		{"invalid resources", func(request *CreateRequest) { request.Resources = Resources{CPU: 4, MemoryGiB: 20} }},
		{"missing main image", func(request *CreateRequest) { request.MainImage = "" }},
		{"mutable init image", func(request *CreateRequest) { request.InitImage = "registry.example/accepted-gate:latest" }},
		{"missing task command", func(request *CreateRequest) { request.Command = nil }},
		{"invalid task environment name", func(request *CreateRequest) { request.Environment = map[string]string{"1INVALID": "value"} }},
		{"long init environment value", func(request *CreateRequest) {
			request.InitContainer.Environment = map[string]string{"VALID": strings.Repeat("x", 257)}
		}},
		{"duplicate volume name", func(request *CreateRequest) { request.WorkVolume.Name = request.SourceVolume.Name }},
		{"noncanonical source volume", func(request *CreateRequest) { request.SourceVolume.Name = "input-data" }},
		{"noncanonical work volume", func(request *CreateRequest) { request.WorkVolume.Name = "cache-data" }},
		{"noncanonical temp volume", func(request *CreateRequest) { request.TempVolume.Name = "scratch-data" }},
		{"missing task mount", func(request *CreateRequest) { request.MainVolumeMounts = request.MainVolumeMounts[:1] }},
		{"unknown task volume", func(request *CreateRequest) { request.MainVolumeMounts[0].Name = "other-data" }},
	}
}
