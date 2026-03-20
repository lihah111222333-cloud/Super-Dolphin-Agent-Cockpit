package provider

import "time"

type Message struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
