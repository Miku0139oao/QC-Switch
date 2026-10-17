package domain

import "time"

type Protocol string

const (
	Messages  Protocol = "messages"
	Responses Protocol = "responses"
	Chat      Protocol = "chat"
)

func (p Protocol) Valid() bool { return p == Messages || p == Responses || p == Chat }

type Model struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Tools     bool     `json:"tools"`
	Vision    bool     `json:"vision"`
	Reasoning bool     `json:"reasoning"`
	Context   int      `json:"context"`
	Efforts   []string `json:"efforts,omitempty"`
}

type Profile struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Endpoint string   `json:"endpoint"`
	Protocol Protocol `json:"protocol"`
	Models   []Model  `json:"models"`
	HasKey   bool     `json:"hasKey"`
	Key      string   `json:"key,omitempty"`
}

type Binding struct {
	Client    string            `json:"client"`
	ProfileID string            `json:"profileId"`
	Model     string            `json:"model"`
	Mode      string            `json:"mode"`
	Directory string            `json:"directory"`
	Roles     map[string]string `json:"roles"`
}

type Settings struct {
	Port          int    `json:"port"`
	Theme         string `json:"theme"`
	RetentionDays int    `json:"retentionDays"`
}

type Record struct {
	ID           int64     `json:"id"`
	Time         time.Time `json:"time"`
	Client       string    `json:"client"`
	Profile      string    `json:"profile"`
	Model        string    `json:"model"`
	Status       int       `json:"status"`
	DurationMS   int64     `json:"durationMs"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
	UsageKnown   bool      `json:"usageKnown"`
	Error        string    `json:"error,omitempty"`
}

type ClientState struct {
	Client   string   `json:"client"`
	Path     string   `json:"path"`
	Managed  bool     `json:"managed"`
	Binding  *Binding `json:"binding,omitempty"`
	Warnings []string `json:"warnings"`
}

type State struct {
	Version       string        `json:"version"`
	Contract      int           `json:"contract"`
	Profiles      []Profile     `json:"profiles"`
	Clients       []ClientState `json:"clients"`
	Settings      Settings      `json:"settings"`
	Running       bool          `json:"running"`
	Address       string        `json:"address"`
	Active        int64         `json:"active"`
	Records       []Record      `json:"records"`
	DataDirectory string        `json:"dataDirectory"`
	Recovery      []string      `json:"recovery"`
}
