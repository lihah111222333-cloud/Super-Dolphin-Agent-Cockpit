package personalization

const (
	profilePreferenceKey      = "personalization.profile"
	maxShortProfileFieldRunes = 80
	maxLongProfileFieldRunes  = 1200
)

type Profile struct {
	DisplayName        string `json:"displayName"`
	Role               string `json:"role"`
	Background         string `json:"background"`
	CustomInstructions string `json:"customInstructions"`
}

type ProfileResult struct {
	Profile Profile `json:"profile"`
}
