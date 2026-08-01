package eci

import "strings"

type invalidCreateRequestCase struct {
	name   string
	mutate func(*CreateRequest)
}

func invalidCreateRequestCases() []invalidCreateRequestCase {
	return append(invalidCreateRequestCoreCases(), invalidCreateRequestVolumeCases()...)
}

func invalidCreateRequestCoreCases() []invalidCreateRequestCase {
	return []invalidCreateRequestCase{
		{"invalid group name", func(request *CreateRequest) { request.ContainerGroupName = "Shard" }},
		{"invalid container name", func(request *CreateRequest) { request.ContainerName = "w" }},
		{"invalid resources", func(request *CreateRequest) {
			request.Resources = Resources{CPU: 4, MemoryGiB: 20}
		}},
		{"missing task command", func(request *CreateRequest) { request.Command = nil }},
		{"too many task commands", func(request *CreateRequest) { request.Command = make([]string, 21) }},
		{"too many task arguments", func(request *CreateRequest) { request.Args = make([]string, 11) }},
		{"invalid task environment name", func(request *CreateRequest) { request.Environment = map[string]string{"1INVALID": "value"} }},
		{"long task environment value", func(request *CreateRequest) {
			request.Environment = map[string]string{"VALID": strings.Repeat("x", 257)}
		}},
		{"invalid init name", func(request *CreateRequest) { request.InitContainer.Name = "-init" }},
		{"duplicate container name", func(request *CreateRequest) { request.InitContainer.Name = request.ContainerName }},
		{"missing init command", func(request *CreateRequest) { request.InitContainer.Command = nil }},
		{"too many init arguments", func(request *CreateRequest) { request.InitContainer.Args = make([]string, 11) }},
		{"invalid init environment name", func(request *CreateRequest) {
			request.InitContainer.Environment = map[string]string{"1INVALID": "value"}
		}},
		{"missing data cache bucket", func(request *CreateRequest) { request.DataCacheBucket = "" }},
		{"reserved data cache bucket", func(request *CreateRequest) { request.DataCacheBucket = "eci-system" }},
		{"invalid base volume name", func(request *CreateRequest) { request.BaseVolume.Name = "Base" }},
		{"relative base volume path", func(request *CreateRequest) { request.BaseVolume.Path = "cache/base" }},
		{"root base volume path", func(request *CreateRequest) { request.BaseVolume.Path = "/" }},
		{"invalid base volume type", func(request *CreateRequest) { request.BaseVolume.Type = "File" }},
	}
}

func invalidCreateRequestVolumeCases() []invalidCreateRequestCase {
	return []invalidCreateRequestCase{
		{"too many DataCache HostPath volumes", func(request *CreateRequest) {
			request.AdditionalBaseVolumes = []HostPathVolume{
				{Name: "base-g002", Path: "/super-dolphin/ci/base/generation-2", Type: "Directory"},
				{Name: "base-g003", Path: "/super-dolphin/ci/base/generation-3", Type: "Directory"},
				{Name: "base-g004", Path: "/super-dolphin/ci/base/generation-4", Type: "Directory"},
				{Name: "base-g005", Path: "/super-dolphin/ci/base/generation-5", Type: "Directory"},
			}
		}},
		{"invalid additional volume name", func(request *CreateRequest) {
			request.AdditionalBaseVolumes = []HostPathVolume{{Name: "Base", Path: "/super-dolphin/ci/base/generation-2", Type: "Directory"}}
		}},
		{"invalid additional volume path", func(request *CreateRequest) {
			request.AdditionalBaseVolumes = []HostPathVolume{{Name: "base-g002", Path: "relative", Type: "Directory"}}
		}},
		{"duplicate additional volume name", func(request *CreateRequest) {
			request.AdditionalBaseVolumes = []HostPathVolume{{Name: request.BaseVolume.Name, Path: "/super-dolphin/ci/base/generation-2", Type: "Directory"}}
		}},
		{"invalid expanded volume name", func(request *CreateRequest) { request.ExpandedVolume.Name = "Expanded" }},
		{"invalid source volume name", func(request *CreateRequest) { request.SourceVolume.Name = "Source" }},
		{"invalid temp volume name", func(request *CreateRequest) { request.TempVolume.Name = "Temp" }},
		{"base and expanded volume names collide", func(request *CreateRequest) { request.ExpandedVolume.Name = request.BaseVolume.Name }},
		{"base and source volume names collide", func(request *CreateRequest) { request.SourceVolume.Name = request.BaseVolume.Name }},
		{"duplicate volume name", func(request *CreateRequest) { request.WorkVolume.Name = request.SourceVolume.Name }},
		{"duplicate temp volume name", func(request *CreateRequest) { request.TempVolume.Name = request.WorkVolume.Name }},
		{"missing task mount", func(request *CreateRequest) { request.MainVolumeMounts = request.MainVolumeMounts[:1] }},
		{"unknown task volume", func(request *CreateRequest) { request.MainVolumeMounts[0].Name = "other-data" }},
		{"task base mount writable", func(request *CreateRequest) { request.MainVolumeMounts[0].ReadOnly = false }},
		{"task expanded mount writable", func(request *CreateRequest) { request.MainVolumeMounts[1].ReadOnly = false }},
		{"task source mount writable", func(request *CreateRequest) { request.MainVolumeMounts[2].ReadOnly = false }},
		{"task work mount readonly", func(request *CreateRequest) { request.MainVolumeMounts[3].ReadOnly = true }},
		{"task temp mount readonly", func(request *CreateRequest) { request.MainVolumeMounts[4].ReadOnly = true }},
		{"init base mount writable", func(request *CreateRequest) { request.InitVolumeMounts[0].ReadOnly = false }},
		{"init expanded mount readonly", func(request *CreateRequest) { request.InitVolumeMounts[1].ReadOnly = true }},
		{"init source mount readonly", func(request *CreateRequest) { request.InitVolumeMounts[2].ReadOnly = true }},
		{"init work mount readonly", func(request *CreateRequest) { request.InitVolumeMounts[3].ReadOnly = true }},
		{"duplicate task mount path", func(request *CreateRequest) {
			request.MainVolumeMounts[0].MountPath = request.MainVolumeMounts[1].MountPath
		}},
		{"absolute task mount subpath", func(request *CreateRequest) { request.MainVolumeMounts[0].SubPath = "/runtime/bin" }},
		{"escaping task mount subpath", func(request *CreateRequest) { request.MainVolumeMounts[0].SubPath = "../runtime/bin" }},
		{"duplicate task volume subpath", func(request *CreateRequest) {
			request.MainVolumeMounts = append(request.MainVolumeMounts,
				VolumeMount{Name: request.ExpandedVolume.Name, MountPath: "/second-expanded", ReadOnly: true},
			)
		}},
		{"relative init mount path", func(request *CreateRequest) { request.InitVolumeMounts[0].MountPath = "workspace" }},
		{"unclean init mount path", func(request *CreateRequest) { request.InitVolumeMounts[0].MountPath = "/input/../workspace" }},
		{"too many tags", func(request *CreateRequest) {
			request.Tags = map[string]string{"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10", "eleven": "11", "twelve": "12", "thirteen": "13", "fourteen": "14", "fifteen": "15", "sixteen": "16", "seventeen": "17", "eighteen": "18", "nineteen": "19", "twenty": "20", "twenty-one": "21"}
		}},
		{"reserved tag key", func(request *CreateRequest) { request.Tags = map[string]string{"acs:reserved": "value"} }},
		{"invalid tag value", func(request *CreateRequest) { request.Tags = map[string]string{"key": "https://invalid"} }},
	}
}
