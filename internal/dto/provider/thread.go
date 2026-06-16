package provider

// ThreadRef is the compact provider thread identity shown in thread listings.
type ThreadRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}
