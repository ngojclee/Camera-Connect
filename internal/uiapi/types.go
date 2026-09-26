package uiapi

// ActionEnvelope is the stable response shape used by CLI mode and UI bindings.
// Keep field names/tags backward-compatible with existing harness automation.
type ActionEnvelope struct {
	OK      bool   `json:"ok"`
	ID      string `json:"id,omitempty"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
	Server  string `json:"server_ts,omitempty"`
}
