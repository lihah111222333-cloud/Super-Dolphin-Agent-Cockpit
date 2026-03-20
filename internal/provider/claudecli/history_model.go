package claudecli

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

type historyLine struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Message   historyMessage `json:"message"`
}

type historyMessage struct {
	Role    string               `json:"role"`
	Content []historyContentItem `json:"content"`
}

type historyContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

const injectedFileHintsHeader = "The user has attached the following files. Use the Read tool to view them:"
