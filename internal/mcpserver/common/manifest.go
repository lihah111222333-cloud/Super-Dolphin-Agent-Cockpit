package common

type ToolManifest struct {
	Name        string
	Description string
	Schema      map[string]any
}

type FamilyManifest struct {
	Family string
	Tools  []ToolManifest
}
