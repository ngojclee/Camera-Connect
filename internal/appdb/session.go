package appdb

// Status snapshot exposed to the agent/UI.
type StatusSnapshot struct {
	LoggedIn    bool   `json:"logged_in"`
	Email       string `json:"email"`
	DeviceLabel string `json:"device_label"`
}

// Status returns (loggedIn, email, deviceLabel) without network calls.
func (s *Service) Status() (bool, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		s.restore()
	}
	if s.session == nil {
		return false, "", ""
	}
	label := ""
	if s.ctx != nil {
		label = s.ctx.MachineLabel
	}
	return true, s.session.Email, label
}
