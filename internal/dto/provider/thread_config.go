package provider

type ThreadConfigPatch struct {
	Model       *string `json:"model,omitempty"`
	Personality *string `json:"personality,omitempty"`
	Approvals   *string `json:"approvals,omitempty"`
}
