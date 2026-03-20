package provider

type ThreadRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}
